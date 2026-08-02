package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A long alternating session over the relay.
//
// The reported conflicts did not appear on the first sync — they turned up
// after people had been playing across two machines for a while. Short tests
// cannot see that: a merge-base that drifts slightly out of step each round,
// or a push record that is never cleared, only becomes a false conflict once
// enough rounds have gone by.
//
// Nobody edits at the same time here, so not one round is a genuine
// divergence and not one may raise a conflict. The base is checked every
// round as well, because a base quietly stranded behind both sides is the
// state that manufactures conflicts later even while sync still looks fine.
func TestSoak_ManyAlternatingRoundsNeverConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test; skipped under -short")
	}

	a, b, gameID := trackBothOverRelay(t, "Soak", map[string]string{"slot1.sav": "round-0"})

	const rounds = 8
	for round := 1; round <= rounds; round++ {
		// A's turn.
		time.Sleep(syncSettleWindow)
		want := fmt.Sprintf("a-round-%d", round)
		a.WriteSave("slot1.sav", want)
		if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
			ca, _ := conflictOn(a, gameID)
			t.Fatalf("round %d: A's one-sided edit conflicted after %d clean rounds: %+v",
				round, round-1, ca.DiffFiles)
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave("slot1.sav") == want }) {
			t.Fatalf("round %d: A's edit never reached B", round)
		}

		// B's turn.
		time.Sleep(syncSettleWindow)
		want = fmt.Sprintf("b-round-%d", round)
		b.WriteSave("slot1.sav", want)
		if status, _ := syncTo(b, gameID, a.NodeID()); status == "conflict" {
			cb, _ := conflictOn(b, gameID)
			t.Fatalf("round %d: B's one-sided edit conflicted: %+v", round, cb.DiffFiles)
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return a.ReadSave("slot1.sav") == want }) {
			t.Fatalf("round %d: B's edit never reached A", round)
		}

		// Neither device may be left holding a conflict between rounds.
		if c, ok := conflictOn(a, gameID); ok {
			t.Fatalf("round %d: A is holding a conflict after a clean exchange: %+v", round, c.DiffFiles)
		}
		if c, ok := conflictOn(b, gameID); ok {
			t.Fatalf("round %d: B is holding a conflict after a clean exchange: %+v", round, c.DiffFiles)
		}
	}

	// After all that, the two devices must genuinely agree — and both must
	// have recorded a merge-base. A missing one means every future edit is
	// judged by mtimes instead of content, which is the blind window the
	// merge-base exists to close.
	if a.ReadSave("slot1.sav") != b.ReadSave("slot1.sav") {
		t.Errorf("after %d rounds the devices hold different saves: A=%q B=%q",
			rounds, a.ReadSave("slot1.sav"), b.ReadSave("slot1.sav"))
	}
	if base := a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()); base == "" {
		t.Error("A holds no merge-base after a long clean session")
	}
	if base := b.Daemon.Store.GetAgreedHash(gameID, a.NodeID()); base == "" {
		t.Error("B holds no merge-base after a long clean session")
	}
}

// The same shape with files being added and removed rather than edited, since
// additions and deletions travel different paths through the decision code
// than content changes do.
func TestSoak_AddingAndDeletingFilesNeverConflicts(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test; skipped under -short")
	}

	a, b, gameID := trackBothOverRelay(t, "SoakFiles", map[string]string{"keep.sav": "constant"})

	for round := 1; round <= 4; round++ {
		name := fmt.Sprintf("slot%d.sav", round)

		time.Sleep(syncSettleWindow)
		a.WriteSave(name, "added by A")
		if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
			ca, _ := conflictOn(a, gameID)
			// Capture why. A one-sided add can only be read as a divergence
			// if the merge-base is behind BOTH sides, so the bases are the
			// evidence that matters, not the file list.
			t.Fatalf("round %d: adding %s conflicted: %+v\n"+
				"  A base=%.12q pushed=%.12q\n"+
				"  B base=%.12q pushed=%.12q\n"+
				"  A holds %s=%q, B holds %s=%q",
				round, name, ca.DiffFiles,
				a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()),
				a.Daemon.Store.GetPushedHash(gameID, b.NodeID()),
				b.Daemon.Store.GetAgreedHash(gameID, a.NodeID()),
				b.Daemon.Store.GetPushedHash(gameID, a.NodeID()),
				name, a.ReadSave(name), name, b.ReadSave(name))
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave(name) == "added by A" }) {
			t.Fatalf("round %d: %s never reached B", round, name)
		}

		// B removes it again; a deletion must propagate rather than being
		// pulled back by the other side as a "missing" file.
		time.Sleep(syncSettleWindow)
		b.RemoveSave(name)
		if status, _ := syncTo(b, gameID, a.NodeID()); status == "conflict" {
			cb, _ := conflictOn(b, gameID)
			t.Fatalf("round %d: deleting %s conflicted: %+v", round, name, cb.DiffFiles)
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return a.ReadSave(name) == "" }) {
			t.Fatalf("round %d: the deletion of %s never reached A — the file came back", round, name)
		}
	}

	if a.ReadSave("keep.sav") != "constant" || b.ReadSave("keep.sav") != "constant" {
		t.Error("the untouched file did not survive the add/delete rounds")
	}
}
