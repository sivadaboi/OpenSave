package e2e

import (
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// The mechanism behind the reported "it says the other person changed it when
// they didn't" conflicts.
//
// After this device pushes, it deliberately leaves its merge-base behind: the
// peer is the side that ends up holding the new state, so convergence is
// recorded when the peer reports back. That report crosses the network. Over a
// relay it can be dropped — and a base frozen behind BOTH sides doesn't just
// delay a conflict, it manufactures one: DetectConflict asks whether both
// sides moved off the base, and once the base is behind both, the answer is
// yes forever.
//
// The state a lost report leaves behind is injected directly rather than
// simulated with a lossy relay: the store IS the thing the report would have
// written, so writing it back is an exact model and not an approximation.
// It has to be injected immediately before the sync, because a background
// sync in any earlier gap repairs it through the same-files path while the
// two sides still hold identical content.
func TestStrandedBase_LostReportThenAnOrdinaryEditMustNotConflict(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "Strand", map[string]string{"slot1.sav": "start"})

	// The merge-base is recorded when the peer reports back, which happens
	// after the files land — so waiting for the save to arrive is not enough
	// to know it exists yet. Under a loaded test run that gap is wide enough
	// to read an empty base and fail before the test has begun.
	var baseBefore string
	if !testutil.WaitFor(30*time.Second, func() bool {
		baseBefore = a.Daemon.Store.GetAgreedHash(gameID, b.NodeID())
		return baseBefore != ""
	}) {
		t.Fatal("no merge-base recorded after the initial sync")
	}

	time.Sleep(syncSettleWindow)
	a.WriteSave("newfile.sav", "added by A")
	syncTo(a, gameID, b.NodeID())
	if !testutil.WaitFor(30*time.Second, func() bool {
		return b.ReadSave("newfile.sav") == "added by A"
	}) {
		t.Fatal("the push never landed, so there is no lost report to model")
	}

	// What the peer ended up holding is exactly what this device pushed, and
	// is what the push record names while the push is unconfirmed.
	pushedState := manifestHashOf(t, b, gameID)

	// One ordinary one-sided edit. The peer is not touched.
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "a-only-edit")

	// The report never arrived: the base is still the pre-push state, and the
	// push is still outstanding. Both are set here — recording a base clears
	// the push record, so the record has to be restored after it, and a lost
	// report would never have cleared it in the first place.
	a.Daemon.Store.SetAgreedHash(gameID, b.NodeID(), baseBefore)
	a.Daemon.Store.SetPushedHash(gameID, b.NodeID(), pushedState)

	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Fatalf("a one-sided edit was reported as a conflict because the merge-base "+
			"was stranded by a lost report: %d differing, %+v", ca.DiffTotal, ca.DiffFiles)
	}
}

// The repair must be exactly as narrow as its proof.
//
// It advances the base only to a state the peer is observed holding AND this
// device is known to have handed over — a genuine common ancestor. If the peer
// has moved on from that state under its own steam while this device also
// changed something, that is a real two-sided divergence and must still stop
// and ask. A repair that swallowed this would silently overwrite somebody's
// save, which is far worse than the prompt it set out to remove.
func TestStrandedBase_RepairStillLetsAGenuineConflictThrough(t *testing.T) {
	a, b, gameID := trackBothOverRelay(t, "StrandReal", map[string]string{"slot1.sav": "start"})

	// Same wait as above: the base lands after the files do.
	var baseBefore string
	if !testutil.WaitFor(30*time.Second, func() bool {
		baseBefore = a.Daemon.Store.GetAgreedHash(gameID, b.NodeID())
		return baseBefore != ""
	}) {
		t.Fatal("no merge-base recorded after the initial sync")
	}

	time.Sleep(syncSettleWindow)
	a.WriteSave("newfile.sav", "added by A")
	syncTo(a, gameID, b.NodeID())
	if !testutil.WaitFor(30*time.Second, func() bool {
		return b.ReadSave("newfile.sav") == "added by A"
	}) {
		t.Fatal("the push never landed")
	}

	// Both sides now genuinely change the same save, independently.
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "a-version")
	b.WriteSave("slot1.sav", "b-version")

	a.Daemon.Store.SetAgreedHash(gameID, b.NodeID(), baseBefore)

	status, _ := syncTo(a, gameID, b.NodeID())
	if status != "conflict" {
		t.Fatalf("both devices edited the same save independently but sync reported %q — "+
			"the stranded-base repair must not reach past what it can prove", status)
	}
}
