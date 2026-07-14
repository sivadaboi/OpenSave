package p2p

import (
	"fmt"
	"sync"

	"github.com/opensave/opensave/relay"
)

// RelayHost runs an OpenSave relay server inside this process when the
// user enables "host a relay" — so friends can connect directly to this
// machine without a third-party relay. Safe for concurrent use.
type RelayHost struct {
	mu      sync.Mutex
	server  *relay.Server
	port    int
	addr    string
	logf    func(level, msg string)
}

// NewRelayHost creates an idle relay host.
func NewRelayHost(logf func(level, msg string)) *RelayHost {
	return &RelayHost{logf: logf}
}

// Apply starts, stops, or restarts the hosted relay to match the desired
// state. Idempotent: calling it repeatedly with the same args is a no-op.
func (h *RelayHost) Apply(enabled bool, port int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !enabled {
		h.stopLocked()
		return
	}
	if h.server != nil && h.port == port {
		return // already running on this port
	}
	h.stopLocked()

	srv := relay.New(relay.Config{Port: port})
	addr, err := srv.Start()
	if err != nil {
		h.logf("error", fmt.Sprintf("could not host relay on port %d: %v", port, err))
		return
	}
	h.server = srv
	h.port = port
	h.addr = addr
	h.logf("success", fmt.Sprintf("hosting WAN relay on port %d — share your IP + this port with friends", port))
}

// Stop shuts the hosted relay down.
func (h *RelayHost) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
}

func (h *RelayHost) stopLocked() {
	if h.server != nil {
		h.server.Stop()
		h.logf("info", "stopped hosted WAN relay")
		h.server = nil
		h.addr = ""
		h.port = 0
	}
}

// Running reports whether a relay is currently hosted and on what port.
func (h *RelayHost) Running() (bool, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.server != nil, h.port
}
