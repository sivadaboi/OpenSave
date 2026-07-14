// Package discovery implements LAN peer discovery over UDP broadcast +
// multicast, wire-compatible with the JS app's discovery.js: JSON
// "opensave-ping" datagrams on port 8385 every 3 seconds, peers expiring
// after 20 seconds of silence.
package discovery

import (
	"encoding/json"
	"net"
	"sync"
	"time"
)

const (
	// DefaultPort is the discovery UDP port (fixed protocol constant).
	DefaultPort      = 8385
	multicastAddress = "224.0.0.1"

	broadcastInterval = 3 * time.Second
	cleanupInterval   = 5 * time.Second
	peerExpiry        = 20 * time.Second
)

// Ping is the discovery datagram, matching the JS wire format exactly.
type Ping struct {
	Type       string `json:"type"` // always "opensave-ping"
	NodeID     string `json:"nodeId"`
	DeviceName string `json:"deviceName"`
	DeviceType string `json:"deviceType"`
	Port       int    `json:"port"`
}

// Discovered is a peer seen on the LAN (not necessarily paired).
type Discovered struct {
	ID         string `json:"id"`
	DeviceName string `json:"deviceName"`
	DeviceType string `json:"deviceType"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	IsWan      bool   `json:"isWan"`
	LastSeen   int64  `json:"lastSeen"` // unix ms
}

// Identity supplies the local device info to announce; queried each
// broadcast so settings changes take effect live.
type Identity func() Ping

// Callbacks let the P2P engine react to discovery events.
type Callbacks struct {
	// OnPeerSeen fires for every valid ping from another device (both new
	// discoveries and refreshes). The engine uses it to flip paired peers
	// online.
	OnPeerSeen func(d Discovered, isNew bool)
	// OnExpired fires when a discovered peer ages out.
	OnExpired func(d Discovered)
}

// Manager runs the discovery loop. Safe for concurrent use.
type Manager struct {
	identity Identity
	cb       Callbacks
	port     int

	mu     sync.Mutex
	peers  map[string]Discovered // key "addr:port"
	conn   *net.UDPConn
	closed chan struct{}
	wg     sync.WaitGroup
}

// New creates a Manager announcing on the standard discovery port.
func New(identity Identity, cb Callbacks) *Manager {
	return &Manager{identity: identity, cb: cb, port: DefaultPort, peers: map[string]Discovered{}}
}

// NewOnPort creates a Manager on a custom UDP port (tests).
func NewOnPort(identity Identity, cb Callbacks, port int) *Manager {
	return &Manager{identity: identity, cb: cb, port: port, peers: map[string]Discovered{}}
}

// Start binds the UDP socket and begins announcing/listening.
func (m *Manager) Start() error {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: m.port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	m.conn = conn
	m.closed = make(chan struct{})

	// Best-effort multicast join on every interface (mirrors the JS
	// addMembership loop; failures are non-fatal since subnet broadcast
	// still works).
	if pc := multicastJoin(conn); pc != nil {
		_ = pc
	}

	m.wg.Add(3)
	go m.readLoop()
	go m.broadcastLoop()
	go m.cleanupLoop()
	return nil
}

// Stop shuts the manager down.
func (m *Manager) Stop() {
	if m.conn == nil {
		return
	}
	close(m.closed)
	m.conn.Close()
	m.wg.Wait()
	m.conn = nil
}

// DiscoveredPeers returns the current LAN peer list.
func (m *Manager) DiscoveredPeers() []Discovered {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Discovered, 0, len(m.peers))
	for _, d := range m.peers {
		out = append(out, d)
	}
	return out
}

func (m *Manager) readLoop() {
	defer m.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, remote, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-m.closed:
				return
			default:
				continue
			}
		}

		var ping Ping
		if err := json.Unmarshal(buf[:n], &ping); err != nil || ping.Type != "opensave-ping" {
			continue
		}

		self := m.identity()
		if ping.NodeID == self.NodeID {
			continue // our own broadcast echoed back
		}
		if isLocalAddress(remote.IP) {
			continue
		}

		d := Discovered{
			ID:         ping.NodeID,
			DeviceName: ping.DeviceName,
			DeviceType: orDefault(ping.DeviceType, "desktop"),
			Address:    remote.IP.String(),
			Port:       ping.Port,
			LastSeen:   time.Now().UnixMilli(),
		}

		key := d.Address + ":" + itoa(d.Port)
		m.mu.Lock()
		_, existed := m.peers[key]
		m.peers[key] = d
		m.mu.Unlock()

		if m.cb.OnPeerSeen != nil {
			m.cb.OnPeerSeen(d, !existed)
		}
	}
}

func (m *Manager) broadcastLoop() {
	defer m.wg.Done()
	m.broadcastPresence() // announce immediately on startup
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.closed:
			return
		case <-ticker.C:
			m.broadcastPresence()
		}
	}
}

func (m *Manager) broadcastPresence() {
	ping := m.identity()
	ping.Type = "opensave-ping"
	raw, err := json.Marshal(ping)
	if err != nil {
		return
	}

	// Subnet broadcast per interface + multicast + limited broadcast.
	targets := broadcastTargets()
	targets = append(targets,
		net.UDPAddr{IP: net.ParseIP(multicastAddress), Port: m.port},
		net.UDPAddr{IP: net.IPv4bcast, Port: m.port},
	)
	for _, target := range targets {
		t := target
		t.Port = m.port
		_, _ = m.conn.WriteToUDP(raw, &t)
	}
}

func (m *Manager) cleanupLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.closed:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-peerExpiry).UnixMilli()
			var expired []Discovered
			m.mu.Lock()
			for key, d := range m.peers {
				if d.LastSeen < cutoff {
					delete(m.peers, key)
					expired = append(expired, d)
				}
			}
			m.mu.Unlock()
			if m.cb.OnExpired != nil {
				for _, d := range expired {
					m.cb.OnExpired(d)
				}
			}
		}
	}
}

// broadcastTargets computes the subnet broadcast address for every active
// IPv4 interface.
func broadcastTargets() []net.UDPAddr {
	var targets []net.UDPAddr
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ip := ipNet.IP.To4()
			mask := ipNet.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			bcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bcast[i] = ip[i] | ^mask[i]
			}
			targets = append(targets, net.UDPAddr{IP: bcast})
		}
	}
	return targets
}

func isLocalAddress(ip net.IP) bool {
	if ip.IsLoopback() {
		return false // loopback is NOT filtered: two daemons on one machine (tests) must see each other
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func multicastJoin(conn *net.UDPConn) error {
	// net.ListenUDP on 0.0.0.0 receives subnet broadcasts already; joining
	// the multicast group extends reach on networks that filter broadcast.
	// Best-effort via the x/net-free approach: a second listener isn't
	// needed because 224.0.0.1 is the all-hosts group that most stacks
	// deliver without an explicit join. Explicit IGMP joins can be added
	// with golang.org/x/net/ipv4 if field reports show missed discovery.
	return nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func itoa(n int) string {
	if n <= 0 {
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
