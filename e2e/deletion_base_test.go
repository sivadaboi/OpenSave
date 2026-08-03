package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/testutil"
)

// A deletion propagated by a peer is applied straight to the filesystem: the
// peer calls delete-file, the file is removed, done. The receiving side never
// runs a sync for it, so nothing updates its merge-base — which is left
// describing a state that still contains the deleted file.
//
// That is the stranded-base bug again, reached from the other direction. The
// push case is repaired by the record of what was handed over; this one has no
// such record, because the receiver pushed nothing. It happens to right itself
// whenever a later sync runs, which is why it is invisible in a quiet test and
// showed up only under load, as a one-sided file add being reported as a
// conflict several rounds later.
//
// Auto-sync is switched off here so that later repair cannot happen: what is
// being tested is whether applying the deletion leaves the base correct, not
// whether something else eventually fixes it.
func TestDeletionBase_APeerDeletionMustNotStrandTheReceiversBase(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "DelBase", map[string]string{
		"keep.sav":   "constant",
		"doomed.sav": "delete me",
	})

	// Let the pair settle, then stop anything further from running on its own.
	time.Sleep(syncSettleWindow)
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) != ""
	}) {
		t.Fatal("no merge-base recorded after the initial sync")
	}
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	bGameID := gameID
	b.API(http.MethodPatch, "/api/games/"+bGameID, map[string]any{"autoSync": false}, nil)

	// B deletes a save and propagates it.
	b.RemoveSave("doomed.sav")
	syncToEventually(b, gameID, a.NodeID())
	if !testutil.WaitFor(45*time.Second, func() bool { return a.ReadSave("doomed.sav") == "" }) {
		t.Fatal("the deletion never reached A")
	}

	// A now holds fewer files than its merge-base describes. Allow a moment
	// for the bookkeeping that should follow the deletion.
	settled := testutil.WaitFor(20*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) == peerHeldHash(t, a, gameID)
	})
	if !settled {
		t.Errorf("after applying a peer's deletion, A's merge-base still describes the "+
			"pre-deletion state (base=%.12q, actual=%.12q) — every later one-sided change "+
			"will be read as a two-way divergence",
			a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()), peerHeldHash(t, a, gameID))
	}
}

// The consequence the user actually sees. With the base stranded by a
// deletion, the next ordinary edit on the OTHER device is reported as a
// conflict on a save nobody else touched — which is the complaint this whole
// investigation started from.
func TestDeletionBase_AnEditAfterAPeerDeletionMustNotConflict(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "DelBaseEdit", map[string]string{
		"keep.sav":   "constant",
		"doomed.sav": "delete me",
	})

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)

	b.RemoveSave("doomed.sav")
	syncToEventually(b, gameID, a.NodeID())
	if !testutil.WaitFor(45*time.Second, func() bool { return a.ReadSave("doomed.sav") == "" }) {
		t.Fatal("the deletion never reached A")
	}

	// Wait for the bookkeeping that follows the deletion. The window is not
	// zero by design: deletions arrive one file at a time and each is applied
	// on its own, so the base can only be correct once the last one has been
	// accounted for. What matters is that it converges on its own rather than
	// waiting for some unrelated later sync to come along — with auto-sync
	// off, nothing else here will.
	if !testutil.WaitFor(20*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) != "" &&
			a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) == peerHeldHash(t, a, gameID)
	}) {
		t.Fatal("A's merge-base never caught up after the peer's deletion")
	}

	// One ordinary edit on A. B is not touched again.
	a.WriteSave("keep.sav", "edited by A alone")
	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Errorf("a one-sided edit after the peer deleted a file was reported as a "+
			"conflict: %d differing, %+v", ca.DiffTotal, ca.DiffFiles)
	}
}

// peerHeldHash is the manifest hash a daemon's save folder currently hashes
// to -- what its merge-base should equal once the two sides agree.
func peerHeldHash(t *testing.T, td *testutil.TestDaemon, gameID string) string {
	t.Helper()
	g, err := td.Daemon.Store.GetGame(gameID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := delta.BuildManifest(g.SavePath)
	if err != nil {
		t.Fatal(err)
	}
	return m.ManifestHash()
}
