package syncengine

import (
	"context"
	"sync"
	"testing"
	"time"
)

// gatedTransport wraps the real fake so a sync can be held open while a second
// request is queued behind it, and records which peer each pass was aimed at.
type gatedTransport struct {
	*fakeTransport

	mu      sync.Mutex
	asked   []string      // peer ids, in the order passes reached the transport
	entered chan struct{} // closed once the first pass is inside
	release chan struct{} // the first pass waits on this
	once    sync.Once
}

func (g *gatedTransport) FetchManifest(ctx context.Context, peer Peer, gameID string, q ManifestQuery) (ManifestResponse, error) {
	g.mu.Lock()
	g.asked = append(g.asked, peer.ID)
	first := len(g.asked) == 1
	g.mu.Unlock()

	if first {
		g.once.Do(func() { close(g.entered) })
		<-g.release
	}
	return g.fakeTransport.FetchManifest(ctx, peer, gameID, q)
}

func (g *gatedTransport) peersAsked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.asked...)
}

// A sync requested while another is running is queued, and the follow-up must
// work out who is reachable when it actually runs.
//
// It used to inherit the peer list from whichever sync it queued behind. That
// list can be minutes old by the time the follow-up starts: a device may have
// dropped, come back on another address, or moved between LAN and relay. The
// follow-up then pushed to an address nobody was listening on, failed inside a
// goroutine with nothing watching it, and the caller had already been told
// "queued and will sync right after" — so the change simply never left, with
// no error anywhere.
func TestQueuedFollowUpResolvesPeersWhenItRuns(t *testing.T) {
	env := setupEngine(t)
	gated := &gatedTransport{
		fakeTransport: env.transport,
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	env.engine.Transport = gated

	write(t, env.localDir, "save.dat", "local")

	stale := Peer{ID: "peer_gone", Name: "Gone", Address: "127.0.0.1", Port: 1}
	fresh := Peer{ID: "peer_here", Name: "Here", Address: "127.0.0.1", Port: 2}

	// What the engine should consult when the follow-up runs.
	var mu sync.Mutex
	current := []Peer{stale}
	env.engine.OnlinePeers = func() []Peer {
		mu.Lock()
		defer mu.Unlock()
		return append([]Peer(nil), current...)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = env.engine.SyncGame(context.Background(), "game1", []Peer{stale})
	}()

	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first sync never reached the transport")
	}

	// Queued behind the running pass.
	if _, err := env.engine.SyncGame(context.Background(), "game1", []Peer{stale}); err != ErrSyncQueued {
		t.Fatalf("second request err = %v, want ErrSyncQueued", err)
	}

	// The world moves on while the first pass is still going.
	mu.Lock()
	current = []Peer{fresh}
	mu.Unlock()

	close(gated.release)
	<-done

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		asked := gated.peersAsked()
		if len(asked) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	asked := gated.peersAsked()
	if len(asked) < 2 {
		t.Fatalf("the queued follow-up never ran: passes = %v", asked)
	}
	if asked[1] != fresh.ID {
		t.Errorf("follow-up synced with %q, want %q — it reused the peer list from "+
			"the sync it queued behind instead of resolving them again", asked[1], fresh.ID)
	}
}

// With nobody reachable the follow-up must say so rather than return quietly.
// Doing nothing looks exactly like having synced, and the caller was promised
// the change would go out.
func TestQueuedFollowUpSaysSoWhenNobodyIsReachable(t *testing.T) {
	env := setupEngine(t)
	gated := &gatedTransport{
		fakeTransport: env.transport,
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	env.engine.Transport = gated

	var logMu sync.Mutex
	var warnings []string
	env.engine.Log = func(level, msg string) {
		logMu.Lock()
		defer logMu.Unlock()
		if level == "warn" {
			warnings = append(warnings, msg)
		}
	}

	write(t, env.localDir, "save.dat", "local")
	peer := Peer{ID: "peer_gone", Name: "Gone", Address: "127.0.0.1", Port: 1}
	env.engine.OnlinePeers = func() []Peer { return nil }

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = env.engine.SyncGame(context.Background(), "game1", []Peer{peer})
	}()

	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first sync never reached the transport")
	}
	if _, err := env.engine.SyncGame(context.Background(), "game1", []Peer{peer}); err != ErrSyncQueued {
		t.Fatalf("second request err = %v, want ErrSyncQueued", err)
	}
	close(gated.release)
	<-done

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		logMu.Lock()
		n := len(warnings)
		logMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	logMu.Lock()
	defer logMu.Unlock()
	if len(warnings) == 0 {
		t.Error("a queued change that reached nobody was not reported at all")
	}
	if n := len(gated.peersAsked()); n != 1 {
		t.Errorf("transport passes = %d, want 1 — the follow-up should not have tried a stale peer", n)
	}
}
