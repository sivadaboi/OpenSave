// Package pairing implements the P2P pairing handshake state:
// incoming requests awaiting user approval, and a 2-minute grace window
// guarding /approve-confirm against unsolicited spoofing — ported from the
// handshake logic in src/daemon/p2p/routes.js.
package pairing

import (
	"sync"
	"time"
)

// graceWindow is how long an initiated handshake stays valid for the
// counterpart's approve-confirm.
const graceWindow = 2 * time.Minute

// IncomingRequest is a handshake from another device awaiting the local
// user's approval.
type IncomingRequest struct {
	PeerID     string `json:"peerId"`
	DeviceName string `json:"deviceName"`
	DeviceType string `json:"deviceType"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	ReceivedAt int64  `json:"receivedAt"` // unix ms
	IsWan      bool   `json:"isWan"`
}

// Manager is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	incoming map[string]IncomingRequest // keyed by peerId
	sent     map[string]time.Time       // keys: peerId, ip, "ip:port"
	now      func() time.Time
}

// New creates a Manager.
func New() *Manager {
	return &Manager{
		incoming: map[string]IncomingRequest{},
		sent:     map[string]time.Time{},
		now:      time.Now,
	}
}

// RecordIncoming stores (or refreshes) a handshake awaiting approval.
func (m *Manager) RecordIncoming(req IncomingRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	req.ReceivedAt = m.now().UnixMilli()
	m.incoming[req.PeerID] = req
}

// TakeIncoming removes and returns a pending request (approval or
// rejection consumes it).
func (m *Manager) TakeIncoming(peerID string) (IncomingRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.incoming[peerID]
	if ok {
		delete(m.incoming, peerID)
	}
	return req, ok
}

// HasIncoming reports whether a handshake from this peer is pending.
func (m *Manager) HasIncoming(peerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.incoming[peerID]
	return ok
}

// PendingRequests lists handshakes awaiting user approval.
func (m *Manager) PendingRequests() []IncomingRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IncomingRequest, 0, len(m.incoming))
	for _, req := range m.incoming {
		out = append(out, req)
	}
	return out
}

// RecordSent marks that we initiated pairing toward the given keys
// (peer id and/or address forms), opening the approve-confirm grace
// window.
func (m *Manager) RecordSent(keys ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for _, k := range keys {
		if k != "" {
			m.sent[k] = now
		}
	}
}

// ValidateConfirm decides whether an /approve-confirm may be accepted:
// we initiated a handshake to this peer/address within the grace window,
// OR they have an active incoming handshake here (simultaneous initiation),
// OR they're already paired (idempotent re-confirm). alreadyPaired is
// supplied by the caller from the store. Valid confirms consume the sent
// entries.
func (m *Manager) ValidateConfirm(peerID, clientIP string, port int, alreadyPaired bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if alreadyPaired {
		return true
	}
	if _, ok := m.incoming[peerID]; ok {
		return true
	}

	now := m.now()
	fresh := func(key string) bool {
		t, ok := m.sent[key]
		return ok && now.Sub(t) < graceWindow
	}

	keys := candidateKeys(peerID, clientIP, port)
	for _, k := range keys {
		if fresh(k) {
			// Consume every candidate entry to prevent replay.
			for _, kk := range keys {
				delete(m.sent, kk)
			}
			return true
		}
	}
	return false
}

// candidateKeys covers peer-id, plain-ip, ip:port, and the
// localhost/127.0.0.1 alias cross-checks from the JS implementation.
func candidateKeys(peerID, clientIP string, port int) []string {
	keys := []string{
		peerID,
		clientIP,
		hostPort(clientIP, port),
	}
	switch clientIP {
	case "localhost":
		keys = append(keys, "127.0.0.1", hostPort("127.0.0.1", port))
	case "127.0.0.1", "::1":
		keys = append(keys, "localhost", hostPort("localhost", port))
	}
	return keys
}

func hostPort(host string, port int) string {
	if port <= 0 {
		return host
	}
	return host + ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
