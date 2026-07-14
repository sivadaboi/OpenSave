package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/version"
)

const (
	wanHeartbeatInterval = 3 * time.Second
	wanReconnectDelay    = 5 * time.Second
	wanRequestTimeout    = 30 * time.Second
	wanPeerExpiry        = 20 * time.Second
)

// RelayMessage is the WAN relay wire envelope, matching wan-client.js.
type RelayMessage struct {
	Type        string          `json:"type"`
	To          string          `json:"to,omitempty"`
	From        string          `json:"from,omitempty"`
	MsgID       string          `json:"msgId,omitempty"`
	Route       string          `json:"route,omitempty"`
	Method      string          `json:"method,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
	Status      int             `json:"status,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	DeviceName  string          `json:"deviceName,omitempty"`
	DeviceType  string          `json:"deviceType,omitempty"`
	Port        int             `json:"port,omitempty"`
	Games       json.RawMessage `json:"games,omitempty"`
	PairedPeers []string        `json:"pairedPeers,omitempty"`
	GameID      string          `json:"gameId,omitempty"`
	EventType   string          `json:"eventType,omitempty"`
	AppVersion  string          `json:"appVersion,omitempty"`
	BuildTimeMs int64           `json:"buildTimeMs,omitempty"`
	EventData   json.RawMessage `json:"data2,omitempty"` // unused; sync-event reuses Data
}

// WanPeer is a device seen in the relay room.
type WanPeer struct {
	ID         string `json:"id"`
	DeviceName string `json:"deviceName"`
	DeviceType string `json:"deviceType"`
	Address    string `json:"address"` // always "relay"
	Port       int    `json:"port"`
	IsWan      bool   `json:"isWan"`
	LastSeen   int64  `json:"lastSeen"`
}

// WanClient maintains the relay room connection: presence, discovery,
// request/response RPC tunneling, and reconnection.
type WanClient struct {
	engine *Engine

	mu          sync.Mutex
	conn        *websocket.Conn
	state       string // disconnected | connecting | connected | error
	lastError   string
	discovered  map[string]WanPeer
	pending     map[string]chan RelayMessage
	generation  int // bumped on every (re)connect to invalidate stale loops
	stopped     bool
	cancelConn  context.CancelFunc
}

func newWanClient(e *Engine) *WanClient {
	return &WanClient{
		engine:     e,
		state:      "disconnected",
		discovered: map[string]WanPeer{},
		pending:    map[string]chan RelayMessage{},
	}
}

// Connect (re)establishes the relay connection based on current settings.
// An empty sync code disconnects.
func (w *WanClient) Connect() {
	settings, err := w.engine.Store.GetSettings()
	if err != nil {
		return
	}
	if settings.SyncCode == "" {
		w.Disconnect()
		return
	}

	w.mu.Lock()
	w.stopped = false
	w.generation++
	gen := w.generation
	if w.cancelConn != nil {
		w.cancelConn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelConn = cancel
	w.state = "connecting"
	w.lastError = ""
	w.mu.Unlock()

	// Let dashboards show "connecting…" immediately rather than a stale
	// disconnected/error state while the dial is in flight.
	w.engine.notifyPeerUpdate()

	go w.run(ctx, gen, settings)
}

// Disconnect closes the relay connection and marks WAN peers offline.
func (w *WanClient) Disconnect() {
	w.mu.Lock()
	w.stopped = true
	w.generation++
	if w.cancelConn != nil {
		w.cancelConn()
		w.cancelConn = nil
	}
	w.state = "disconnected"
	w.lastError = ""
	w.discovered = map[string]WanPeer{}
	w.mu.Unlock()

	w.markWanPeersOffline()
	w.engine.notifyPeerUpdate()
}

func (w *WanClient) markWanPeersOffline() {
	peers, err := w.engine.Store.ListPeers()
	if err != nil {
		return
	}
	for _, p := range peers {
		if p.Address == "relay" && p.Status != "offline" {
			p.Status = "offline"
			_ = w.engine.Store.UpsertPeer(p)
		}
	}
}

func (w *WanClient) run(ctx context.Context, gen int, settings store.Settings) {
	wsURL := fmt.Sprintf("%s/?room=%s&device=%s",
		settings.RelayURL, url.QueryEscape(settings.SyncCode), url.QueryEscape(settings.DeviceName))
	w.engine.Log("info", fmt.Sprintf("connecting to WAN relay %s (room %s)", settings.RelayURL, settings.SyncCode))

	dialCtx, cancelDial := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	cancelDial()
	if err != nil {
		w.connectionLost(ctx, gen, "error", err.Error())
		return
	}
	conn.SetReadLimit(16 * 1024 * 1024) // block batches are ~1.5MB of base64 + overhead

	w.mu.Lock()
	if gen != w.generation {
		w.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "superseded")
		return
	}
	w.conn = conn
	w.state = "connected"
	w.lastError = ""
	w.mu.Unlock()

	w.engine.Log("success", "connected to WAN relay")

	// Announce presence.
	pairedIDs := w.pairedPeerIDs()
	w.send(RelayMessage{
		Type: "hello", From: w.localPeerID(),
		DeviceName: settings.DeviceName, DeviceType: settings.DeviceType, Port: settings.Port,
		Games: w.gamesStateJSON(), PairedPeers: pairedIDs,
		AppVersion: version.Version, BuildTimeMs: version.BuildTimeMs(),
	})
	w.engine.notifyPeerUpdate()

	// Heartbeat + expiry sweeps.
	go func() {
		ticker := time.NewTicker(wanHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s, err := w.engine.Store.GetSettings()
				if err != nil {
					continue
				}
				w.send(RelayMessage{
					Type: "ping", From: w.localPeerID(),
					DeviceName: s.DeviceName, DeviceType: s.DeviceType, Port: s.Port,
					Games: w.gamesStateJSON(),
					AppVersion: version.Version, BuildTimeMs: version.BuildTimeMs(),
				})
				w.expireStalePeers()
			}
		}
	}()

	// Read loop.
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			w.connectionLost(ctx, gen, "disconnected", "")
			return
		}
		var msg RelayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		w.handleMessage(ctx, msg)
	}
}

// connectionLost schedules a reconnect unless the client was stopped.
func (w *WanClient) connectionLost(ctx context.Context, gen int, state, errMsg string) {
	w.mu.Lock()
	if gen != w.generation {
		w.mu.Unlock()
		return
	}
	w.conn = nil
	w.state = state
	w.lastError = errMsg
	stopped := w.stopped
	w.mu.Unlock()

	w.engine.notifyPeerUpdate()
	if stopped {
		return
	}
	if errMsg != "" {
		// x509 failures against a healthy relay are almost always transient
		// (free-tier relay waking up serving a stale cert) or a wrong local
		// clock — say so instead of leaving a scary bare TLS error.
		if strings.Contains(errMsg, "x509") || strings.Contains(errMsg, "certificate") {
			errMsg += " — the relay may still be waking up (retrying automatically); if this persists, check this device's date & time"
		}
		w.engine.Log("warn", "WAN relay error: "+errMsg)
	}
	w.engine.Log("info", "WAN relay connection lost; reconnecting in 5s")

	select {
	case <-ctx.Done():
		// canceled by a newer Connect()/Disconnect(); they own the state now
	case <-time.After(wanReconnectDelay):
		w.Connect()
	}
}

func (w *WanClient) send(msg RelayMessage) {
	w.mu.Lock()
	conn := w.conn
	w.mu.Unlock()
	if conn == nil {
		return
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, raw)
}

// SendRelayMessage exposes raw sends for the WAN transport / conflict
// resolution paths.
func (w *WanClient) SendRelayMessage(msg RelayMessage) { w.send(msg) }

// Request performs an HTTP-shaped RPC against a peer through the relay.
func (w *WanClient) Request(ctx context.Context, peerID, route, method string, body any) (json.RawMessage, error) {
	w.mu.Lock()
	if w.conn == nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("WAN relay connection is currently offline")
	}
	msgID := fmt.Sprintf("msg_%d_%06d", time.Now().UnixMilli(), rand.Intn(1_000_000))
	respCh := make(chan RelayMessage, 1)
	w.pending[msgID] = respCh
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		delete(w.pending, msgID)
		w.mu.Unlock()
	}()

	var rawBody json.RawMessage
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rawBody = raw
	}

	w.send(RelayMessage{
		Type: "request", To: peerID, From: w.localPeerID(),
		MsgID: msgID, Route: route, Method: method, Body: rawBody,
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wanRequestTimeout):
		return nil, fmt.Errorf("WAN request timeout on route %s", route)
	case resp := <-respCh:
		if resp.Status >= 200 && resp.Status < 300 {
			return resp.Data, nil
		}
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(resp.Data, &errBody)
		if errBody.Error == "" {
			errBody.Error = fmt.Sprintf("WAN request returned status %d", resp.Status)
		}
		return nil, fmt.Errorf("%s", errBody.Error)
	}
}

// Status reports the room state for the dashboard's Internet Sync page.
func (w *WanClient) Status() map[string]any {
	settings, _ := w.engine.Store.GetSettings()

	w.mu.Lock()
	state := w.state
	lastError := w.lastError
	connected := w.conn != nil
	peers := make([]map[string]any, 0, len(w.discovered))
	now := time.Now().UnixMilli()
	for _, p := range w.discovered {
		_, pairedErr := w.engine.Store.GetPeer(p.ID)
		peers = append(peers, map[string]any{
			"id": p.ID, "deviceName": p.DeviceName, "deviceType": p.DeviceType,
			"address": p.Address, "port": p.Port, "isWan": true, "lastSeen": p.LastSeen,
			"paired": pairedErr == nil,
			"online": now-p.LastSeen < wanPeerExpiry.Milliseconds(),
		})
	}
	w.mu.Unlock()

	var errField any
	if lastError != "" {
		errField = lastError
	}
	return map[string]any{
		"enabled":     settings.SyncCode != "",
		"connected":   connected,
		"state":       state,
		"error":       errField,
		"relayUrl":    settings.RelayURL,
		"roomCode":    settings.SyncCode,
		"localPeerId": w.localPeerID(),
		"peers":       peers,
	}
}

// DiscoveredWanPeers returns the current room members.
func (w *WanClient) DiscoveredWanPeers() []WanPeer {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]WanPeer, 0, len(w.discovered))
	for _, p := range w.discovered {
		out = append(out, p)
	}
	return out
}

func (w *WanClient) expireStalePeers() {
	cutoff := time.Now().Add(-wanPeerExpiry).UnixMilli()
	changed := false

	w.mu.Lock()
	for id, p := range w.discovered {
		if p.LastSeen < cutoff {
			delete(w.discovered, id)
			changed = true
		}
	}
	w.mu.Unlock()

	// Paired relay-routed peers go offline after silence too.
	peers, err := w.engine.Store.ListPeers()
	if err == nil {
		for _, p := range peers {
			if p.Address == "relay" && p.Status == "online" && p.LastSeenMs < cutoff {
				p.Status = "offline"
				_ = w.engine.Store.UpsertPeer(p)
				changed = true
			}
		}
	}
	if changed {
		w.engine.notifyPeerUpdate()
	}
}

func (w *WanClient) localPeerID() string {
	settings, err := w.engine.Store.GetSettings()
	if err != nil {
		return ""
	}
	return settings.NodeID
}

func (w *WanClient) pairedPeerIDs() []string {
	peers, err := w.engine.Store.ListPeers()
	if err != nil {
		return nil
	}
	ids := make([]string, len(peers))
	for i, p := range peers {
		ids[i] = p.ID
	}
	return ids
}

func (w *WanClient) gamesStateJSON() json.RawMessage {
	raw, err := json.Marshal(w.engine.LocalGamesState())
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}
