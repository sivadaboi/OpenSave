package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Three devices in one room. Nothing in this suite has ever used more than
// two, and several things that are correct for a pair are not obviously
// correct for a trio.
//
// The merge-base and the push record are both keyed by (game, peer), so a
// device syncing with two others keeps two of each. A push to the laptop must
// not be readable as a push to the deck, and convergence with one must not
// clear or advance what is recorded for the other — either mistake makes a
// device that is genuinely behind look caught up, which is the exact shape
// that suppresses a conflict that should have been raised.
func TestThreeDevices_PerPeerStateStaysSeparate(t *testing.T) {
	if testing.Short() {
		t.Skip("three-daemon test; skipped under -short")
	}
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, "Trio-A")
	b := testutil.NewTestDaemon(t, "Trio-B")
	c := testutil.NewTestDaemon(t, "Trio-C")

	pairOverRelay(t, a, b, relayURL, "trio-room")
	pairOverRelay(t, a, c, relayURL, "trio-room")

	a.WriteSave("slot1.sav", "start")
	gameID := a.TrackGame("Trio")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Trio", "savePath": b.SaveDir}, nil)
	c.API(http.MethodPost, "/api/games", map[string]string{"name": "Trio", "savePath": c.SaveDir}, nil)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(90*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "start" && c.ReadSave("slot1.sav") == "start"
	}) {
		t.Fatal("the initial state never reached both peers")
	}

	// Both peers must be tracked independently.
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) != "" &&
			a.Daemon.Store.GetAgreedHash(gameID, c.NodeID()) != ""
	}) {
		t.Fatalf("A did not record a merge-base for both peers (B=%q C=%q)",
			a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()),
			a.Daemon.Store.GetAgreedHash(gameID, c.NodeID()))
	}

	// A pushes to B only. C is deliberately left behind.
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "for-b-only")
	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Fatalf("a one-sided edit conflicted with B: %+v", ca.DiffFiles)
	}
	if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave("slot1.sav") == "for-b-only" }) {
		t.Fatal("the edit never reached B")
	}

	// Syncing with C afterwards must also be conflict-free: C never changed
	// anything, so this is still a one-sided edit from C's point of view.
	// Getting the per-peer bookkeeping wrong shows up right here, as a
	// conflict against a device that has been sitting still the whole time.
	status, _ := syncTo(a, gameID, c.NodeID())
	if status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Errorf("syncing with the untouched third device raised a conflict — per-peer "+
			"state is leaking between peers: %+v", ca.DiffFiles)
	}
	if !testutil.WaitFor(60*time.Second, func() bool { return c.ReadSave("slot1.sav") == "for-b-only" }) {
		t.Error("the edit never reached the third device")
	}

	// And all three must end up holding the same save.
	if a.ReadSave("slot1.sav") != b.ReadSave("slot1.sav") ||
		a.ReadSave("slot1.sav") != c.ReadSave("slot1.sav") {
		t.Errorf("the three devices disagree: A=%q B=%q C=%q",
			a.ReadSave("slot1.sav"), b.ReadSave("slot1.sav"), c.ReadSave("slot1.sav"))
	}
}

// A genuine three-way divergence: every device edits while apart. Each pair
// must be judged on its own, and resolving with one peer must not be taken as
// having resolved with the other.
func TestThreeDevices_AGenuineDivergenceIsRaisedPerPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("three-daemon test; skipped under -short")
	}
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, "TrioDiv-A")
	b := testutil.NewTestDaemon(t, "TrioDiv-B")
	c := testutil.NewTestDaemon(t, "TrioDiv-C")

	pairOverRelay(t, a, b, relayURL, "trio-div")
	pairOverRelay(t, a, c, relayURL, "trio-div")

	a.WriteSave("slot1.sav", "shared")
	gameID := a.TrackGame("TrioDiv")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "TrioDiv", "savePath": b.SaveDir}, nil)
	c.API(http.MethodPost, "/api/games", map[string]string{"name": "TrioDiv", "savePath": c.SaveDir}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(90*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "shared" && c.ReadSave("slot1.sav") == "shared"
	}) {
		t.Fatal("the initial state never reached both peers")
	}

	// All three edit the same file independently.
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "a-version")
	b.WriteSave("slot1.sav", "b-version")
	c.WriteSave("slot1.sav", "c-version")

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
		t.Errorf("A and B both edited the same save but sync reported %q", status)
	}
	// Whatever happened with B, C is still a separate disagreement and must
	// be raised on its own rather than inherited or skipped.
	if status, _ := syncTo(a, gameID, c.NodeID()); status != "conflict" {
		t.Errorf("A and C both edited the same save but sync reported %q — the second "+
			"peer's divergence was not judged separately", status)
	}
}
