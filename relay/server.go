// Package relay implements the OpenSave WAN relay: a stateless WebSocket
// room broker (clients joining with the same ?room= code relay messages to
// each other) plus the Google Drive OAuth proxy that keeps the client
// secret server-side — a port of src/relay-server.js, wire-compatible with
// both the Go and JS OpenSave clients.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opensave/opensave/internal/version"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Config tunes the relay; zero values take the JS defaults.
type Config struct {
	Port              int           // default 8386
	MaxPerRoom        int           // default 20
	HeartbeatInterval time.Duration // default 30s
	// GoogleClientSecret enables the /api/oauth/token proxy for Google
	// Drive; empty disables it with a config error response.
	GoogleClientSecret string
}

func (c *Config) applyDefaults() {
	// Port 0 stays 0: the OS assigns a free port (the production binary
	// passes 8386 via its PORT env default).
	if c.MaxPerRoom == 0 {
		c.MaxPerRoom = 20
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 30 * time.Second
	}
}

// Server is one relay instance.
type Server struct {
	cfg Config

	mu               sync.Mutex
	rooms            map[string]map[*client]struct{}
	totalConnections int64
	totalMessages    int64
	droppedMessages  int64
	startedAt        time.Time

	// totalQueued is the byte size of all outbound messages buffered across
	// every client, so one busy room can't consume the whole instance.
	totalQueued atomic.Int64

	httpServer *http.Server
	listener   net.Listener
}

// Queue budgets. The read limit below allows a 16 MB frame, so bounding the
// outbound queue by message *count* bounds nothing useful: 256 slots x 16 MB
// is gigabytes per slow client, and the relay runs on a 512 MB instance.
//
// The per-client figure is not arbitrary. A syncing peer has
// ConcurrencyFor(true) block requests outstanding, each answered with a batch
// of BatchIndices' target size, inflated by a third on the wire by base64 —
// currently 8 x 2 MB x 4/3, about 22 MB arriving at once. Anything below that
// sheds responses during a perfectly healthy transfer, and each shed response
// costs the requester a full retry. Keep this comfortably above that product;
// if the sync constants change, this has to move with them.
const (
	maxQueuedBytesPerClient = 32 << 20  // 32 MiB: ~22 MiB in flight plus headroom
	maxQueuedBytesTotal     = 256 << 20 // 256 MiB across every room
	sendQueueSlots          = 64
)

type client struct {
	conn       *websocket.Conn
	deviceName string
	send       chan []byte
	srv        *Server
	// queued is the byte size of everything currently sitting in send.
	// Senders reserve against the budget before committing to the channel,
	// so the cap is never exceeded even under concurrent relays.
	queued atomic.Int64
}

// enqueue hands raw to the client's writer, or reports false when doing so
// would exceed the per-client or global byte budget. A drop is safe: the
// sync protocol already retries a block fetch that doesn't answer, whereas
// buffering without limit takes the whole relay down for everyone.
func (c *client) enqueue(raw []byte) bool {
	size := int64(len(raw))
	if c.queued.Add(size) > maxQueuedBytesPerClient {
		c.queued.Add(-size)
		return false
	}
	if c.srv.totalQueued.Add(size) > maxQueuedBytesTotal {
		c.srv.totalQueued.Add(-size)
		c.queued.Add(-size)
		return false
	}
	select {
	case c.send <- raw:
		return true
	default:
		c.srv.totalQueued.Add(-size)
		c.queued.Add(-size)
		return false
	}
}

// release accounts for a message once the writer has taken it off the queue.
func (c *client) release(raw []byte) {
	size := int64(len(raw))
	c.queued.Add(-size)
	c.srv.totalQueued.Add(-size)
}

// drain releases every message still queued for a client that is going away.
// Callers hold Server.mu, which is what makes this safe against a concurrent
// enqueue.
func (c *client) drain() {
	for {
		select {
		case raw := <-c.send:
			c.release(raw)
		default:
			return
		}
	}
}

// New creates a relay server.
func New(cfg Config) *Server {
	cfg.applyDefaults()
	return &Server{cfg: cfg, rooms: map[string]map[*client]struct{}{}, startedAt: time.Now()}
}

// Start listens and serves until Stop. Returns the bound address.
func (s *Server) Start() (string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/oauth/token", s.handleOAuthProxy)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", s.cfg.Port))
	if err != nil {
		return "", err
	}
	s.listener = ln
	s.httpServer = &http.Server{Handler: mux}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("[Relay] server error: %v\n", err)
		}
	}()
	return ln.Addr().String(), nil
}

// Stop shuts the relay down.
func (s *Server) Stop() {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
	}
}

// handleRoot serves the health payload on "/" (like the JS server) and
// upgrades WebSocket requests into room membership.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		s.handleWS(w, r)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.handleHealth(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	roomCount := len(s.rooms)
	clientCount := 0
	for _, room := range s.rooms {
		clientCount += len(room)
	}
	totals := map[string]any{
		"status":           "ok",
		"version":          version.Version,
		"uptime":           time.Since(s.startedAt).Seconds(),
		"startedAt":        s.startedAt.UTC().Format(time.RFC3339),
		"rooms":            roomCount,
		"clients":          clientCount,
		"totalConnections": s.totalConnections,
		"totalMessages":    s.totalMessages,
		// Queue health: a non-zero drop count means a client couldn't keep up
		// and the sync protocol had to retry, which is the signal that the
		// budgets below are too tight for real traffic.
		"droppedMessages": s.droppedMessages,
		"queuedBytes":     s.totalQueued.Load(),
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(totals)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	roomCode := r.URL.Query().Get("room")
	deviceName := r.URL.Query().Get("device")
	if deviceName == "" {
		deviceName = "Unknown Device"
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	// coder/websocket's default read limit is 32 KB — smaller than a single
	// sync block message (up to ~2 MB of save data, base64-encoded, plus
	// envelope; the app client allows 16 MB, see wanclient.go). Without
	// this, the relay kills a peer's connection the moment a real save
	// transfer starts: manifests (small JSON) pass, every block payload
	// drops the link, and syncs die in an endless reconnect-retry loop.
	conn.SetReadLimit(16 << 20)
	if roomCode == "" {
		conn.Close(websocket.StatusCode(4001), "Missing 'room' parameter")
		return
	}

	c := &client{conn: conn, deviceName: deviceName, send: make(chan []byte, sendQueueSlots), srv: s}

	s.mu.Lock()
	room, ok := s.rooms[roomCode]
	if !ok {
		room = map[*client]struct{}{}
		s.rooms[roomCode] = room
	}
	if len(room) >= s.cfg.MaxPerRoom {
		s.mu.Unlock()
		conn.Close(websocket.StatusCode(4002), "Room is full")
		return
	}
	room[c] = struct{}{}
	s.totalConnections++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if room, ok := s.rooms[roomCode]; ok {
			delete(room, c)
			if len(room) == 0 {
				delete(s.rooms, roomCode)
			}
		}
		// Give back whatever is still queued, or a churn of disconnects
		// would slowly eat the global budget until nothing could be relayed.
		c.drain()
		s.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := r.Context()

	// Writer: drains the send channel.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-c.send:
				if !ok {
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, raw)
				cancel()
				c.release(raw)
				if err != nil {
					return
				}
			}
		}
	}()

	// Reader: every message relays verbatim to all other room members.
	// (coder/websocket answers protocol pings internally, covering the JS
	// server's heartbeat behavior.)
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		// Enqueue under the same lock that owns room membership: that way a
		// client being torn down can't be handed a message after its queue
		// has been drained, which would leak its bytes from the global budget.
		// enqueue never blocks, so this stays a short critical section.
		s.mu.Lock()
		s.totalMessages++
		for other := range s.rooms[roomCode] {
			if other == c {
				continue
			}
			if !other.enqueue(raw) {
				s.droppedMessages++ // slow client: drop rather than stall the room
			}
		}
		s.mu.Unlock()
	}
}

// handleOAuthProxy exchanges/refreshes Google Drive tokens server-side so
// the client secret never ships inside the app.
func (s *Server) handleOAuthProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Provider     string `json:"provider"`
		ClientID     string `json:"client_id"`
		GrantType    string `json:"grant_type"`
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		RedirectURI  string `json:"redirect_uri"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if payload.Provider != "google_drive" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Provider %q is not supported by this proxy.", payload.Provider),
		})
		return
	}
	if s.cfg.GoogleClientSecret == "" {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
			"error": "OAuth Proxy configuration error: Client Secret is not set on the relay server.",
		})
		return
	}

	form := make(map[string][]string)
	form["client_id"] = []string{payload.ClientID}
	form["client_secret"] = []string{s.cfg.GoogleClientSecret}
	form["grant_type"] = []string{payload.GrantType}
	switch payload.GrantType {
	case "authorization_code":
		form["code"] = []string{payload.Code}
		form["code_verifier"] = []string{payload.CodeVerifier}
		form["redirect_uri"] = []string{payload.RedirectURI}
	case "refresh_token":
		form["refresh_token"] = []string{payload.RefreshToken}
	}

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
			"error": "OAuth Proxy failed to exchange token: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
