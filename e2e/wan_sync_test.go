package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/relay"
	"github.com/opensave/opensave/testutil"
)

// startRelay boots an in-process relay server on a random port and returns
// its ws:// URL.
func startRelay(t *testing.T) string {
	t.Helper()
	srv := relay.New(relay.Config{Port: 0})
	// Port 0 isn't supported by Config default logic (0 -> 8386), so pick a
	// free port explicitly via net.Listen semantics: use a high random port.
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return "ws://" + addr
}

// joinRoom points a daemon at the relay with the given sync code.
func joinRoom(td *testutil.TestDaemon, relayURL, code string) {
	td.API(http.MethodPost, "/api/settings", map[string]any{
		"relayUrl": relayURL, "syncCode": code,
	}, nil)
}

func TestWanRoomDiscoveryAndPairing(t *testing.T) {
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, "Wan-A")
	b := testutil.NewTestDaemon(t, "Wan-B")

	joinRoom(a, relayURL, "test-room-1")
	joinRoom(b, relayURL, "test-room-1")

	// Both should discover each other through the room.
	if !testutil.WaitFor(15*time.Second, func() bool {
		return len(a.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1 &&
			len(b.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1
	}) {
		t.Fatal("room members never discovered each other")
	}

	// A pairs with B through the relay.
	a.API(http.MethodPost, "/api/peers/pair", map[string]any{"peerId": b.NodeID(), "address": "relay"}, nil)

	if !testutil.WaitFor(10*time.Second, func() bool {
		return len(b.Daemon.P2P.Pairing.PendingRequests()) > 0
	}) {
		t.Fatal("WAN handshake never arrived")
	}
	b.API(http.MethodPost, "/api/peers/approve", map[string]any{"peerId": a.NodeID()}, nil)

	if !testutil.WaitFor(10*time.Second, func() bool {
		pa, errA := a.Daemon.Store.GetPeer(b.NodeID())
		pb, errB := b.Daemon.Store.GetPeer(a.NodeID())
		return errA == nil && errB == nil && pa.Address == "relay" && pb.Address == "relay"
	}) {
		t.Fatal("WAN pairing did not complete with relay-routed addresses on both sides")
	}
}

func TestWanFullSync(t *testing.T) {
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, "Wan-Sync-A")
	b := testutil.NewTestDaemon(t, "Wan-Sync-B")

	joinRoom(a, relayURL, "sync-room")
	joinRoom(b, relayURL, "sync-room")

	if !testutil.WaitFor(15*time.Second, func() bool {
		return len(a.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1 &&
			len(b.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1
	}) {
		t.Fatal("discovery failed")
	}

	// Pair through the relay.
	a.API(http.MethodPost, "/api/peers/pair", map[string]any{"peerId": b.NodeID(), "address": "relay"}, nil)
	if !testutil.WaitFor(10*time.Second, func() bool {
		return len(b.Daemon.P2P.Pairing.PendingRequests()) > 0
	}) {
		t.Fatal("handshake failed")
	}
	b.API(http.MethodPost, "/api/peers/approve", map[string]any{"peerId": a.NodeID()}, nil)
	if !testutil.WaitFor(10*time.Second, func() bool {
		_, errA := a.Daemon.Store.GetPeer(b.NodeID())
		_, errB := b.Daemon.Store.GetPeer(a.NodeID())
		return errA == nil && errB == nil
	}) {
		t.Fatal("pairing failed")
	}

	// A has a game with data; B pre-tracks at its own dir.
	a.WriteSave("slot1.sav", "wan save data")
	gameID := a.TrackGame("Wan Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Wan Game", "savePath": b.SaveDir}, nil)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	// The file must arrive at B entirely through the relay tunnel.
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "wan save data"
	}) {
		t.Fatalf("B never received the file over WAN; got %q", b.ReadSave("slot1.sav"))
	}

	// WAN status endpoint reflects the connected room.
	var status struct {
		Connected bool   `json:"connected"`
		RoomCode  string `json:"roomCode"`
	}
	a.API(http.MethodGet, "/api/wan/status", nil, &status)
	if !status.Connected || status.RoomCode != "sync-room" {
		t.Errorf("wan status = %+v, want connected in sync-room", status)
	}
}

// TestWanLargeFileSync pushes a save big enough that block messages far
// exceed coder/websocket's 32 KB default read limit — the original
// TestWanFullSync payload (13 bytes) fit inside one tiny message, which
// is how the relay's missing SetReadLimit shipped: manifests synced,
// every real save transfer killed the connection.
func TestWanLargeFileSync(t *testing.T) {
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, "Wan-Big-A")
	b := testutil.NewTestDaemon(t, "Wan-Big-B")

	joinRoom(a, relayURL, "big-room")
	joinRoom(b, relayURL, "big-room")
	if !testutil.WaitFor(15*time.Second, func() bool {
		return len(a.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1 &&
			len(b.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1
	}) {
		t.Fatal("discovery failed")
	}
	a.API(http.MethodPost, "/api/peers/pair", map[string]any{"peerId": b.NodeID(), "address": "relay"}, nil)
	if !testutil.WaitFor(10*time.Second, func() bool {
		return len(b.Daemon.P2P.Pairing.PendingRequests()) > 0
	}) {
		t.Fatal("handshake failed")
	}
	b.API(http.MethodPost, "/api/peers/approve", map[string]any{"peerId": a.NodeID()}, nil)
	if !testutil.WaitFor(10*time.Second, func() bool {
		_, errA := a.Daemon.Store.GetPeer(b.NodeID())
		_, errB := b.Daemon.Store.GetPeer(a.NodeID())
		return errA == nil && errB == nil
	}) {
		t.Fatal("pairing failed")
	}

	// ~3 MB of pseudo-random (incompressible) data: several full-size
	// delta blocks, each one a WS message far beyond the 32 KB default.
	big := make([]byte, 3<<20)
	rnd := uint32(12345)
	for i := range big {
		rnd = rnd*1664525 + 1013904223
		big[i] = byte(rnd >> 24)
	}
	a.WriteSave("bigdata.bin", string(big))
	gameID := a.TrackGame("Wan Big Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Wan Big Game", "savePath": b.SaveDir}, nil)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(90*time.Second, func() bool {
		return b.ReadSave("bigdata.bin") == string(big)
	}) {
		got := len(b.ReadSave("bigdata.bin"))
		t.Fatalf("3MB file never arrived intact over the relay (got %d of %d bytes)", got, len(big))
	}
}
