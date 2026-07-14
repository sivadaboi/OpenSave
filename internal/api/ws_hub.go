package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub broadcasts daemon state changes to connected dashboard clients over
// WebSocket, mirroring the JS daemon's message types: init (full state on
// connect), games-update, peers-update, sync-start/progress/complete/error,
// and log entries.
type Hub struct {
	mu      sync.Mutex
	clients map[*hubClient]struct{}
	// InitPayload builds the full-state "init" message for a new client.
	InitPayload func() any
}

type hubClient struct {
	conn *websocket.Conn
	send chan []byte
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{clients: map[*hubClient]struct{}{}}
}

// Message is the wire envelope, matching the JS shape {type, data}.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Broadcast sends a typed message to every connected client.
func (h *Hub) Broadcast(msgType string, data any) {
	raw, err := json.Marshal(Message{Type: msgType, Data: data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- raw:
		default: // slow client: drop the message rather than block the daemon
		}
	}
}

// ServeHTTP upgrades the request to a WebSocket and services it until the
// client disconnects.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Local dashboard only; the daemon binds localhost.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	client := &hubClient{conn: conn, send: make(chan []byte, 64)}

	if h.InitPayload != nil {
		if raw, err := json.Marshal(Message{Type: "init", Data: h.InitPayload()}); err == nil {
			writeCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_ = conn.Write(writeCtx, websocket.MessageText, raw)
			cancel()
		}
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := r.Context()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-client.send:
				if !ok {
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, raw)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	// Read loop: the dashboard doesn't send meaningful messages, but
	// reading keeps ping/pong handling alive and detects disconnect.
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

// ClientCount returns the number of connected dashboard clients.
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
