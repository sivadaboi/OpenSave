package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/relay"
	"github.com/opensave/opensave/testutil"
)

// startRelayServer is startRelay's sibling that also hands back the server,
// so a test can read its counters afterwards.
func startRelayServer(t *testing.T) (string, *relay.Server) {
	t.Helper()
	srv := relay.New(relay.Config{Port: 0})
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return "ws://" + addr, srv
}

func relayHealth(t *testing.T, wsURL string) map[string]any {
	t.Helper()
	httpURL := "http://" + wsURL[len("ws://"):] + "/health"
	resp, err := http.Get(httpURL)
	if err != nil {
		t.Fatalf("relay health: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode relay health: %v", err)
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// pairOverRelay joins both daemons to a room and completes pairing through it.
func pairOverRelay(t *testing.T, a, b *testutil.TestDaemon, relayURL, room string) {
	t.Helper()
	joinRoom(a, relayURL, room)
	joinRoom(b, relayURL, room)

	if !testutil.WaitFor(20*time.Second, func() bool {
		return len(a.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1 &&
			len(b.Daemon.P2P.Wan.DiscoveredWanPeers()) >= 1
	}) {
		t.Fatal("room members never discovered each other")
	}
	a.API(http.MethodPost, "/api/peers/pair", map[string]any{"peerId": b.NodeID(), "address": "relay"}, nil)
	if !testutil.WaitFor(15*time.Second, func() bool {
		return len(b.Daemon.P2P.Pairing.PendingRequests()) > 0
	}) {
		t.Fatal("WAN handshake never arrived")
	}
	b.API(http.MethodPost, "/api/peers/approve", map[string]any{"peerId": a.NodeID()}, nil)
	// Online, not merely known: a sync only attempts peers marked online, so
	// returning earlier lets a sync fired straight afterwards do nothing at
	// all and report no error. See PairWith for the same reasoning on LAN.
	if !testutil.WaitFor(20*time.Second, func() bool {
		p1, e1 := a.Daemon.Store.GetPeer(b.NodeID())
		p2, e2 := b.Daemon.Store.GetPeer(a.NodeID())
		return e1 == nil && e2 == nil &&
			p1.Status == "online" && p2.Status == "online"
	}) {
		p1, _ := a.Daemon.Store.GetPeer(b.NodeID())
		p2, _ := b.Daemon.Store.GetPeer(a.NodeID())
		t.Fatalf("WAN pairing did not come online on both sides (a sees %q, b sees %q)",
			p1.Status, p2.Status)
	}
}

// The full new transfer path in one test: blocks streamed to disk as they
// arrive, fetched by a pool of concurrent workers, gzipped on the wire, and
// relayed through a server that now bounds its queue by bytes. A mix of
// highly compressible and incompressible data, so both codec branches run.
func TestWan_LargeMixedFileSyncThroughRelay(t *testing.T) {
	relayURL, relaySrv := startRelayServer(t)
	a := testutil.NewTestDaemon(t, "BigWan-A")
	b := testutil.NewTestDaemon(t, "BigWan-B")
	pairOverRelay(t, a, b, relayURL, "bigfile-room")

	// Compressible: repetitive save-like text. ~12 MB.
	compressible := bytes.Repeat([]byte(`{"slot":1,"gold":9999,"name":"hero","flags":[0,0,1]},`), 240_000)
	// Incompressible: random bytes, which the codec must send raw. ~8 MB.
	incompressible := make([]byte, 8<<20)
	rand.New(rand.NewSource(42)).Read(incompressible)
	// A small file too, so the batching path for 64 KB blocks is covered.
	small := []byte("a modest save file")

	writeRaw(t, a.SaveDir, "bulk/compressible.sav", compressible)
	writeRaw(t, a.SaveDir, "bulk/incompressible.bin", incompressible)
	writeRaw(t, a.SaveDir, "small.sav", small)

	gameID := a.TrackGame("Big WAN Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Big WAN Game", "savePath": b.SaveDir}, nil)

	start := time.Now()
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(180*time.Second, func() bool {
		return len(readRaw(b.SaveDir, "bulk/compressible.sav")) == len(compressible) &&
			len(readRaw(b.SaveDir, "bulk/incompressible.bin")) == len(incompressible) &&
			len(readRaw(b.SaveDir, "small.sav")) == len(small)
	}) {
		t.Fatalf("large WAN sync incomplete after 180s: compressible=%d/%d incompressible=%d/%d small=%d/%d",
			len(readRaw(b.SaveDir, "bulk/compressible.sav")), len(compressible),
			len(readRaw(b.SaveDir, "bulk/incompressible.bin")), len(incompressible),
			len(readRaw(b.SaveDir, "small.sav")), len(small))
	}
	elapsed := time.Since(start)

	// Byte-exact, verified by hash rather than length.
	for _, tc := range []struct {
		rel  string
		want []byte
	}{
		{"bulk/compressible.sav", compressible},
		{"bulk/incompressible.bin", incompressible},
		{"small.sav", small},
	} {
		got := readRaw(b.SaveDir, tc.rel)
		if sha256Hex(got) != sha256Hex(tc.want) {
			t.Errorf("%s: content hash mismatch (%d bytes received, %d expected)", tc.rel, len(got), len(tc.want))
		}
	}

	// No temp files may survive a successful transfer.
	walkForTempFiles(t, b.SaveDir)

	// The relay's byte budget must not have shed anything under a real sync.
	// A drop here is silently paid for as a retry, so it should show up as a
	// deliberate signal rather than as a mysteriously slow transfer.
	health := relayHealth(t, relayURL)
	if dropped, ok := health["droppedMessages"].(float64); ok && dropped > 0 {
		t.Errorf("relay dropped %v messages during a normal large-file sync — the per-client budget is too tight", dropped)
	}
	if queued, ok := health["queuedBytes"].(float64); ok && queued != 0 {
		t.Errorf("relay still has %v bytes charged to its queue after the transfer settled", queued)
	}
	_ = relaySrv

	total := len(compressible) + len(incompressible) + len(small)
	t.Logf("%.1f MB across 3 files in %s (%.1f MB/s) through the relay",
		float64(total)/(1<<20), elapsed.Round(time.Millisecond),
		float64(total)/(1<<20)/elapsed.Seconds())
}

// A large file that changes only slightly must transfer only the changed
// blocks. If this regresses, every autosave of a big save re-uploads the
// whole thing — the single most expensive thing sync can get wrong.
func TestWan_LargeFileDeltaTransfersOnlyChangedBlocks(t *testing.T) {
	relayURL, _ := startRelayServer(t)
	a := testutil.NewTestDaemon(t, "Delta-A")
	b := testutil.NewTestDaemon(t, "Delta-B")
	pairOverRelay(t, a, b, relayURL, "delta-room")

	// 25 MB puts this on the 512 KB block path.
	payload := make([]byte, 25<<20)
	rand.New(rand.NewSource(7)).Read(payload)
	writeRaw(t, a.SaveDir, "big.sav", payload)

	gameID := a.TrackGame("Delta Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Delta Game", "savePath": b.SaveDir}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(180*time.Second, func() bool {
		return sha256Hex(readRaw(b.SaveDir, "big.sav")) == sha256Hex(payload)
	}) {
		t.Fatal("initial large sync never completed")
	}

	before := relayHealth(t, relayURL)["totalMessages"].(float64)

	// Change one block's worth of bytes in the middle.
	time.Sleep(syncSettleWindow)
	copy(payload[12<<20:], []byte("MUTATED-REGION-MUTATED-REGION"))
	writeRaw(t, a.SaveDir, "big.sav", payload)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(120*time.Second, func() bool {
		return sha256Hex(readRaw(b.SaveDir, "big.sav")) == sha256Hex(payload)
	}) {
		t.Fatal("delta sync never converged")
	}

	after := relayHealth(t, relayURL)["totalMessages"].(float64)
	delta := after - before

	// The initial transfer of 25 MB takes ~50 blocks over several batches.
	// A one-block edit must cost far less than that; a full re-send would be
	// a comparable number of messages again.
	if delta > 40 {
		t.Errorf("a single-block edit cost %v relay messages — the delta path looks broken, "+
			"the whole file is probably being re-sent", delta)
	}
	t.Logf("one-block edit in a 25 MB file cost %v relay messages", delta)
}

func writeRaw(t *testing.T, dir, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o666); err != nil {
		t.Fatal(err)
	}
}

func readRaw(dir, rel string) []byte {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return raw
}

// walkForTempFiles fails the test if any .opensave.tmp survived.
func walkForTempFiles(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".tmp" || bytes.HasSuffix([]byte(path), []byte(".opensave.tmp")) {
			t.Errorf("temp file survived a successful sync: %s", path)
		}
		return nil
	})
}
