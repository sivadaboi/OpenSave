package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/testutil"
)

// The stranded-base repair advances the merge-base to a state the peer is
// observed holding AND this device is known to have handed over. That is
// sound only while the push is still outstanding.
//
// It does not stay outstanding forever. A push records the state handed over;
// a later pure pull advances the base to whatever the peer had, without
// touching that record. If the peer then returns to the older pushed state —
// by rolling back to one of its own snapshots, an ordinary thing to do — the
// stale record matches again and the base would be dragged backwards onto it.
//
// The cost is a missing prompt rather than a stale one: with the base back at
// the pushed state the peer looks unchanged, so this device pushes over the
// rollback instead of asking. Whether the two devices agreed on something
// NEWER afterwards is exactly the information the record must not outlive.
//
// Constructed directly in the store rather than by timing two daemons: while
// both are online the rollback simply syncs across and the pair converges, so
// the situation only arises when they are apart, and injecting the state is an
// exact model of "they were apart" without depending on the clock.
func TestStrandedBaseEdge_AStalePushRecordMustNotUndoALaterAgreement(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "EdgeStale", map[string]string{
		"slot1.sav": "original",
	})

	// Stop anything running on its own. The state below is injected to stand
	// for a history that took a while to arrive at, and a background sync
	// firing between the injection and the assertion simply re-records the
	// real merge-base — leaving the test measuring nothing and failing at
	// random depending on when the timer landed.
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	time.Sleep(syncSettleWindow)

	// Whatever B is holding now is the state a rollback would land it on.
	peerHeld := manifestHashOf(t, b, gameID)
	if peerHeld == "" {
		t.Fatal("could not read the peer's current manifest hash")
	}

	// The pair later agreed on something else entirely — a state neither of
	// them is holding any more, which is what a merge-base looks like once
	// both sides have moved on from it.
	// Real order of events: the push is recorded first, and the later pull
	// that advanced the base came after it. Setting them the other way round
	// would not model anything that can actually happen.
	a.Daemon.Store.SetPushedHash(gameID, b.NodeID(), peerHeld)
	laterAgreement := "1111111111111111111111111111111111111111111111111111111111111111"
	a.Daemon.Store.SetAgreedHash(gameID, b.NodeID(), laterAgreement)

	// A has work of its own on top.
	a.WriteSave("slot1.sav", "new-work-by-a")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status != "conflict" {
		t.Errorf("a stale push record pulled the merge-base back onto the peer's state: "+
			"sync reported %q, so the peer's copy is about to be overwritten without a prompt",
			status)
	}
}

// manifestHashOf reads the manifest hash a daemon currently holds for a game,
// which is what the peer would report over the wire.
func manifestHashOf(t *testing.T, td *testutil.TestDaemon, gameID string) string {
	t.Helper()
	game, err := td.Daemon.Store.GetGame(gameID)
	if err != nil {
		t.Fatalf("reading game %q: %v", gameID, err)
	}
	m, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		t.Fatalf("building manifest for %q: %v", game.SavePath, err)
	}
	return m.ManifestHash()
}

// Sanity guard for the repair itself: with the push still outstanding — no
// later agreement recorded — the repair must fire and keep an ordinary
// one-sided edit quiet. Without this, the test above could be "passed" by
// deleting the repair entirely.
func TestStrandedBaseEdge_AnOutstandingPushStillRepairsTheBase(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "EdgeOutstanding", map[string]string{
		"slot1.sav": "original",
	})

	// Same reason as above, and it matters more here: this asserts that NO
	// conflict is raised, so a background sync quietly repairing the base
	// would make the test pass without the repair under test doing anything.
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	time.Sleep(syncSettleWindow)

	peerHeld := manifestHashOf(t, b, gameID)

	// The edit comes first: the state below is what a lost report leaves
	// behind, and it has to be the last thing written before the sync reads
	// it.
	a.WriteSave("slot1.sav", "one-sided-edit")

	// The lost-report state: the base is behind and no convergence has been
	// recorded since, so the push record is still live. It is set last
	// because recording a base deliberately clears it.
	a.Daemon.Store.SetAgreedHash(gameID, b.NodeID(),
		"2222222222222222222222222222222222222222222222222222222222222222")
	a.Daemon.Store.SetPushedHash(gameID, b.NodeID(), peerHeld)

	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Errorf("an outstanding push was not used to repair the stranded base: %+v", ca.DiffFiles)
	}
}
