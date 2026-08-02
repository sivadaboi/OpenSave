package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// trackBothOverRelay pairs two daemons through a relay and gets one game
// tracked and synced on both, so a test can start from "in sync".
func trackBothOverRelay(t *testing.T, name string, files map[string]string) (*testutil.TestDaemon, *testutil.TestDaemon, string) {
	t.Helper()
	relayURL := startRelay(t)
	a := testutil.NewTestDaemon(t, name+"-A")
	b := testutil.NewTestDaemon(t, name+"-B")
	pairOverRelay(t, a, b, relayURL, name+"-room")

	for rel, content := range files {
		a.WriteSave(rel, content)
	}
	gameID := a.TrackGame(name)
	b.API(http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": b.SaveDir}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(60*time.Second, func() bool {
		for rel, content := range files {
			if b.ReadSave(rel) != content {
				return false
			}
		}
		return true
	}) {
		t.Fatal("initial sync over the relay never completed")
	}
	return a, b, gameID
}

// The reported scenario, over the relay rather than the LAN.
//
// The LAN version of this passes, and the users hitting it sync over the
// internet — which is a different path with a different failure mode.
// Convergence after a push is recorded when the peer reports back over the
// relay, and a relay can drop that report where a direct HTTP call cannot.
func TestReportedWAN_AddingOneFileOnOneDeviceMustNotConflict(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "WanAddOne", map[string]string{
		"existing.sav": "already here",
	})

	time.Sleep(syncSettleWindow)
	a.WriteSave("newfile.sav", "added by A")

	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Fatalf("adding one file raised a conflict over the relay: %d differing, %+v",
			ca.DiffTotal, ca.DiffFiles)
	}

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("newfile.sav") == "added by A"
	}) {
		t.Fatal("the added file never reached the peer over the relay")
	}

	// The part the theory is about: after the file has landed, does the next
	// ordinary sync stay quiet, or does a merge-base left behind by the push
	// turn a one-sided edit into a two-way divergence?
	time.Sleep(syncSettleWindow)
	a.WriteSave("existing.sav", "edited by A only")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Fatalf("a later one-sided edit conflicted after a push over the relay — "+
			"this is the reported \"conflicts when the other side hasn't changed\": %d differing, %+v",
			ca.DiffTotal, ca.DiffFiles)
	}
}

// Same again with the peer taking the next turn, since the two directions
// record convergence differently: the pusher waits to be told, the puller
// knows immediately.
func TestReportedWAN_AlternatingEditsMustNotConflict(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "WanAlternate", map[string]string{
		"slot1.sav": "start",
	})

	// Several turns each way. One device edits, it propagates, the other
	// edits. Nobody ever edits at the same time, so nothing here is a
	// genuine divergence and no turn may raise a conflict.
	for round := 0; round < 3; round++ {
		time.Sleep(syncSettleWindow)
		a.WriteSave("slot1.sav", "a-turn")
		if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
			ca, _ := conflictOn(a, gameID)
			t.Fatalf("round %d: A's turn conflicted: %+v", round, ca.DiffFiles)
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave("slot1.sav") == "a-turn" }) {
			t.Fatalf("round %d: A's edit never reached B", round)
		}

		time.Sleep(syncSettleWindow)
		b.WriteSave("slot1.sav", "b-turn")
		if status, _ := syncTo(b, gameID, a.NodeID()); status == "conflict" {
			cb, _ := conflictOn(b, gameID)
			t.Fatalf("round %d: B's turn conflicted: %+v", round, cb.DiffFiles)
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return a.ReadSave("slot1.sav") == "b-turn" }) {
			t.Fatalf("round %d: B's edit never reached A", round)
		}
	}
}

// And the case the theory actually needs: the convergence report is lost.
//
// After a push, this side leaves its merge-base behind and waits for the peer
// to say it caught up. Simulated by stopping the peer's server immediately
// after the push, so the report cannot arrive, then bringing the work back and
// making one ordinary one-sided edit.
func TestReportedWAN_LostConvergenceReportDoesNotStrandTheMergeBase(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "WanLostReport", map[string]string{
		"slot1.sav": "start",
	})

	time.Sleep(syncSettleWindow)
	a.WriteSave("added.sav", "pushed by A")
	syncTo(a, gameID, b.NodeID())

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("added.sav") == "pushed by A"
	}) {
		t.Skip("the push never landed; nothing to say about the report that follows it")
	}

	// Both devices now hold the same files. A one-sided edit from here must
	// not be read as both sides having moved.
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "a-only-edit")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Errorf("a one-sided edit after a completed push was reported as a conflict: %d differing, %+v",
			ca.DiffTotal, ca.DiffFiles)
	}
}
