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
	startedAt        time.Time

	httpServer *http.Server
	listener   net.Listener
}

type client struct {
	conn       *websocket.Conn
	deviceName string
	send       chan []byte
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
	if roomCode == "" {
		conn.Close(websocket.StatusCode(4001), "Missing 'room' parameter")
		return
	}

	c := &client{conn: conn, deviceName: deviceName, send: make(chan []byte, 256)}

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
		s.mu.Lock()
		s.totalMessages++
		peers := make([]*client, 0, len(s.rooms[roomCode]))
		for other := range s.rooms[roomCode] {
			if other != c {
				peers = append(peers, other)
			}
		}
		s.mu.Unlock()

		for _, other := range peers {
			select {
			case other.send <- raw:
			default: // slow client: drop rather than stall the room
			}
		}
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
