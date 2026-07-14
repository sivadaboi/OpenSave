package p2p

import (
	"testing"

	"github.com/opensave/opensave/internal/p2p/syncengine"
)

// TestTrackSyncOutcome verifies the interrupted-sync failsafe queues a game
// when a peer's sync errors and clears it once the sync completes.
func TestTrackSyncOutcome(t *testing.T) {
	e := &Engine{Log: func(string, string) {}}

	// A transient/network error queues the game for automatic retry.
	e.trackSyncOutcome("game1", map[string]syncengine.Result{
		"peerA": {Status: "error"},
	})
	if !e.isPending("game1") {
		t.Fatal("a sync error should queue the game for retry")
	}

	// A later clean completion clears it — no manual retry needed.
	e.trackSyncOutcome("game1", map[string]syncengine.Result{
		"peerA": {Status: "updated"},
	})
	if e.isPending("game1") {
		t.Error("a completed sync should clear the pending retry")
	}

	// A conflict is a user decision, not a network failure — not queued.
	e.trackSyncOutcome("game2", map[string]syncengine.Result{
		"peerA": {Status: "conflict"},
	})
	if e.isPending("game2") {
		t.Error("a conflict must not be queued for silent retry")
	}

	// One failing peer among several still queues the game.
	e.trackSyncOutcome("game3", map[string]syncengine.Result{
		"peerA": {Status: "in_sync"},
		"peerB": {Status: "error"},
	})
	if !e.isPending("game3") {
		t.Error("any failing peer should queue the game")
	}
}

func (e *Engine) isPending(gameID string) bool {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()
	return e.pendingResync[gameID]
}
