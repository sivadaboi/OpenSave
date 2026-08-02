package discovery

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// LAN discovery had no test at all, and "the other device shows as offline
// when it isn't" is a bug that has already been reported once. Every
// assertion here is about the receive path, because that is what decides
// whether a device the user can see across the room appears in the app.

// listener starts a Manager on a free port and returns it with a socket for
// sending pings at it, standing in for another device on the LAN.
func listener(t *testing.T, self Ping, cb Callbacks) (*Manager, *net.UDPConn, int) {
	t.Helper()

	// Ask the OS for a free UDP port, then hand it to the Manager. A fixed
	// port makes this test fail when anything else on the machine (including
	// a real OpenSave) is already listening.
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	m := NewOnPort(func() Ping { return self }, cb, port)
	if err := m.Start(); err != nil {
		t.Fatalf("start discovery on port %d: %v", port, err)
	}
	t.Cleanup(m.Stop)

	sender, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial the manager: %v", err)
	}
	t.Cleanup(func() { sender.Close() })
	return m, sender, port
}

func sendPing(t *testing.T, conn *net.UDPConn, p Ping) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(raw); err != nil {
		t.Fatalf("send ping: %v", err)
	}
}

// seenRecorder collects OnPeerSeen calls without racing the read loop.
type seenRecorder struct {
	mu   sync.Mutex
	seen []Discovered
	news []bool
}

func (r *seenRecorder) record(d Discovered, isNew bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, d)
	r.news = append(r.news, isNew)
}

func (r *seenRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

// waitFor polls until cond holds or the deadline passes. Discovery is
// asynchronous; a fixed sleep is either flaky or slow.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// The basic contract: another device announces itself and the app sees it,
// with the details it announced.
func TestDiscovery_APingFromAnotherDeviceIsDiscovered(t *testing.T) {
	rec := &seenRecorder{}
	self := Ping{NodeID: "me", DeviceName: "My PC", DeviceType: "desktop", Port: 9000}
	m, sender, _ := listener(t, self, Callbacks{OnPeerSeen: rec.record})

	sendPing(t, sender, Ping{
		Type: "opensave-ping", NodeID: "them",
		DeviceName: "Steam Deck", DeviceType: "deck", Port: 8384,
	})

	if !waitFor(5*time.Second, func() bool { return rec.count() > 0 }) {
		t.Fatal("a valid ping from another device was never reported — this is the " +
			"\"device shows offline when it is online\" failure")
	}

	rec.mu.Lock()
	got, isNew := rec.seen[0], rec.news[0]
	rec.mu.Unlock()

	if got.ID != "them" {
		t.Errorf("discovered ID = %q, want %q", got.ID, "them")
	}
	if got.DeviceName != "Steam Deck" {
		t.Errorf("discovered DeviceName = %q, want %q", got.DeviceName, "Steam Deck")
	}
	if got.DeviceType != "deck" {
		t.Errorf("discovered DeviceType = %q, want %q", got.DeviceType, "deck")
	}
	if got.Port != 8384 {
		t.Errorf("discovered Port = %d, want 8384 — the port is what the app connects back on", got.Port)
	}
	if !isNew {
		t.Error("the first sighting of a device was not reported as new")
	}

	// And it must appear in the list the UI reads.
	peers := m.DiscoveredPeers()
	if len(peers) != 1 || peers[0].ID != "them" {
		t.Errorf("DiscoveredPeers() = %+v, want exactly the one device just seen", peers)
	}
}

// A device that keeps announcing must refresh, not re-appear. The engine
// uses isNew to decide whether to react, so a repeat reported as new would
// re-trigger first-sighting work every three seconds.
func TestDiscovery_ARepeatSightingIsNotReportedAsNew(t *testing.T) {
	rec := &seenRecorder{}
	self := Ping{NodeID: "me", Port: 9000}
	_, sender, _ := listener(t, self, Callbacks{OnPeerSeen: rec.record})

	ping := Ping{Type: "opensave-ping", NodeID: "them", DeviceName: "Laptop", Port: 8384}
	sendPing(t, sender, ping)
	if !waitFor(5*time.Second, func() bool { return rec.count() >= 1 }) {
		t.Fatal("the first ping was never seen")
	}
	sendPing(t, sender, ping)
	if !waitFor(5*time.Second, func() bool { return rec.count() >= 2 }) {
		t.Fatal("the second ping was never seen")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.news[0] {
		t.Error("first sighting was not marked new")
	}
	if rec.news[1] {
		t.Error("a repeat announcement from the same device was reported as a new discovery")
	}
}

// Every device receives its own broadcast back. Reporting that would make
// each machine list itself as a peer.
func TestDiscovery_OurOwnBroadcastIsIgnored(t *testing.T) {
	rec := &seenRecorder{}
	self := Ping{NodeID: "me", DeviceName: "My PC", Port: 9000}
	m, sender, _ := listener(t, self, Callbacks{OnPeerSeen: rec.record})

	sendPing(t, sender, Ping{Type: "opensave-ping", NodeID: "me", DeviceName: "My PC", Port: 9000})

	// Give the read loop a fair chance to get it wrong.
	time.Sleep(500 * time.Millisecond)
	if rec.count() != 0 {
		t.Errorf("the device reported its own broadcast as a peer: %+v", rec.seen)
	}
	if len(m.DiscoveredPeers()) != 0 {
		t.Errorf("the device listed itself in DiscoveredPeers: %+v", m.DiscoveredPeers())
	}
}

// Anything else on the port is not ours. The read loop must survive it:
// discovery runs for the life of the app, and a panic here takes LAN sync
// down until restart.
func TestDiscovery_JunkOnThePortIsIgnoredAndDoesNotStopDiscovery(t *testing.T) {
	rec := &seenRecorder{}
	self := Ping{NodeID: "me", Port: 9000}
	_, sender, _ := listener(t, self, Callbacks{OnPeerSeen: rec.record})

	// Not JSON; JSON but not a ping; JSON of the wrong shape; empty.
	for _, junk := range [][]byte{
		[]byte("this is not json at all"),
		[]byte(`{"type":"something-else","nodeId":"x"}`),
		[]byte(`{"type":"opensave-ping","nodeId":12345}`),
		[]byte(`{}`),
		{},
	} {
		if _, err := sender.Write(junk); err != nil {
			t.Fatalf("send junk: %v", err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if rec.count() != 0 {
		t.Errorf("junk on the discovery port was reported as a peer: %+v", rec.seen)
	}

	// The loop must still be alive and still discover a real device.
	sendPing(t, sender, Ping{Type: "opensave-ping", NodeID: "them", DeviceName: "Real", Port: 8384})
	if !waitFor(5*time.Second, func() bool { return rec.count() == 1 }) {
		t.Fatal("discovery stopped working after receiving junk — one malformed " +
			"datagram on the LAN would disable peer discovery until restart")
	}
}

// A device that announces no type is still a device. Defaulting matters
// because the UI picks an icon from it.
func TestDiscovery_MissingDeviceTypeDefaultsToDesktop(t *testing.T) {
	rec := &seenRecorder{}
	_, sender, _ := listener(t, Ping{NodeID: "me", Port: 9000}, Callbacks{OnPeerSeen: rec.record})

	sendPing(t, sender, Ping{Type: "opensave-ping", NodeID: "them", DeviceName: "Unknown", Port: 8384})
	if !waitFor(5*time.Second, func() bool { return rec.count() > 0 }) {
		t.Fatal("the ping was never seen")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.seen[0].DeviceType != "desktop" {
		t.Errorf("DeviceType with none announced = %q, want %q", rec.seen[0].DeviceType, "desktop")
	}
}

// Stop must be safe and complete. It is called on every settings change that
// rebinds discovery, so a Stop that hangs or panics strands the app.
func TestDiscovery_StopIsCleanAndRepeatable(t *testing.T) {
	m, _, _ := listener(t, Ping{NodeID: "me", Port: 9000}, Callbacks{})

	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return — the read/broadcast/cleanup goroutines did not exit")
	}

	// A second Stop (the t.Cleanup one, and any double-stop in the app) must
	// not panic on the already-closed connection.
	m.Stop()
}
