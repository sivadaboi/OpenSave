package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

// Switching to a branch you just made must not empty the save folder.
//
// Reported as "branches don't work". Creating a branch records the name and
// nothing else, so the new branch has no snapshots — and switching cleared
// the save location and then found nothing to restore into it. Every save
// file disappeared. The state was recoverable by switching back, since the
// outgoing branch is snapshotted first, but from the outside the game had
// simply lost its save.
//
// A new branch means "carry on from here", the way it does everywhere else
// that word is used. Until the branch has a state of its own, the files
// already present ARE its starting state.
func TestSwitchToNewBranchKeepsTheCurrentSave(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main progress")
	writeSave(t, env.saveDir, "config/settings.ini", "vsync=1")

	branch, err := env.mgr.CreateBranch("game1", "ng-plus", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatalf("switching to a new branch failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		entries, _ := os.ReadDir(env.saveDir)
		t.Fatalf("the save is gone after switching to a new branch (%d entries left in the "+
			"folder) — the game would start as if it had never been played: %v",
			len(entries), err)
	}
	if string(got) != "main progress" {
		t.Errorf("slot1.sav = %q, want %q", got, "main progress")
	}
	if c, err := os.ReadFile(filepath.Join(env.saveDir, "config", "settings.ini")); err != nil || string(c) != "vsync=1" {
		t.Errorf("nested file did not carry over: %q (%v)", c, err)
	}
}

// The other half of the choice: a branch asked to start empty really is
// empty, and switching to it clears the save folder. That is a deliberate
// fresh run, and the only reason it is safe to do is the backup below.
func TestNewBranchCanDeliberatelyStartEmpty(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main progress")

	branch, err := env.mgr.CreateBranch("game1", "fresh-run", false)
	if err != nil {
		t.Fatal(err)
	}
	if snaps, _ := env.store.ListSnapshots("game1", branch); len(snaps) != 0 {
		t.Errorf("a branch asked to start empty has %d snapshot(s)", len(snaps))
	}
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(env.saveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a deliberately empty branch left %d entries in the save folder", len(entries))
	}

	// And the save that was there is not lost — it is on the branch switched
	// away from, which is what makes the emptying reversible.
	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil || string(got) != "main progress" {
		t.Errorf("the save did not come back on returning to main: %q (%v)", got, err)
	}
}

// The seeded snapshot has to land on the NEW branch, not the one still
// active. Recording it against the current branch would leave the new one
// empty — the exact state that empties the save folder — while looking like
// it had worked.
func TestSeededSnapshotLandsOnTheNewBranch(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main progress")

	before, _ := env.store.ListSnapshots("game1", "main")
	branch, err := env.mgr.CreateBranch("game1", "seeded", true)
	if err != nil {
		t.Fatal(err)
	}

	onNew, _ := env.store.ListSnapshots("game1", branch)
	if len(onNew) != 1 {
		t.Errorf("the new branch has %d snapshot(s), want exactly the seeded one", len(onNew))
	}
	if after, _ := env.store.ListSnapshots("game1", "main"); len(after) != len(before) {
		t.Errorf("seeding added %d snapshot(s) to the branch still active; it must only "+
			"record onto the new one", len(after)-len(before))
	}
	// The game must not have been moved off its branch by the seeding.
	if g, _ := env.store.GetGame("game1"); g.ActiveBranch != "main" {
		t.Errorf("creating a branch moved the active branch to %q", g.ActiveBranch)
	}
}

// If the pre-switch backup cannot be taken, nothing may be cleared.
//
// This used to log the failure and carry on, so the one case where the backup
// mattered most — a full disk, a locked file, a database that would not write
// — was the one where the save folder was emptied with nothing to go back to.
func TestSwitchAbortsWhenTheBackupCannotBeTaken(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "irreplaceable")

	branch, err := env.mgr.CreateBranch("game1", "target", false)
	if err != nil {
		t.Fatal(err)
	}

	// Make the snapshot impossible: a regular FILE where the game's backup
	// directory needs to be, so creating it cannot succeed.
	if err := os.MkdirAll(env.backups, 0o777); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(env.backups, "game1")
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := env.mgr.SwitchBranch("game1", branch); err == nil {
		t.Error("the switch reported success even though the save could not be backed up first")
	}

	// The save must be exactly as it was.
	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatalf("the save was cleared despite the backup failing — this is the data loss "+
			"the abort exists to prevent: %v", err)
	}
	if string(got) != "irreplaceable" {
		t.Errorf("save = %q, want %q", got, "irreplaceable")
	}
	// And the game must still be on the branch it started on.
	if g, _ := env.store.GetGame("game1"); g.ActiveBranch != "main" {
		t.Errorf("the active branch moved to %q despite the switch failing", g.ActiveBranch)
	}
}

// The branch has to actually diverge afterwards: play on the new branch, go
// back, and each side must hold its own state. Carrying the files over must
// not quietly make the two branches the same thing.
func TestBranchesDivergeAfterSwitching(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main progress")

	branch, err := env.mgr.CreateBranch("game1", "experiment", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}

	// Play on the new branch and record it.
	writeSave(t, env.saveDir, "slot1.sav", "experimental progress")
	if _, err := env.mgr.Create("game1", "on the experiment", false); err != nil {
		t.Fatal(err)
	}

	// Back to main: its own state returns.
	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if string(got) != "main progress" {
		t.Errorf("after returning to main, slot1.sav = %q, want %q", got, "main progress")
	}

	// And forward again: the experiment's own state, not main's.
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if string(got) != "experimental progress" {
		t.Errorf("after returning to %q, slot1.sav = %q, want %q", branch, got, "experimental progress")
	}
}

// Switching to a branch that DOES have a state still replaces the save
// folder with it — including removing files the outgoing branch had and the
// incoming one does not. Carrying files over must apply only to a branch
// with no state of its own.
func TestSwitchToPopulatedBranchReplacesTheSave(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main")
	writeSave(t, env.saveDir, "main-only.sav", "only on main")

	branch, err := env.mgr.CreateBranch("game1", "other", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}
	// Give the branch a state that does NOT include main-only.sav.
	if err := os.Remove(filepath.Join(env.saveDir, "main-only.sav")); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot1.sav", "other branch")
	if _, err := env.mgr.Create("game1", "other state", false); err != nil {
		t.Fatal(err)
	}

	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.saveDir, "main-only.sav")); err != nil {
		t.Errorf("main's own file did not come back: %v", err)
	}

	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.saveDir, "main-only.sav")); !os.IsNotExist(err) {
		t.Error("a file that exists only on main survived the switch to a branch without it — " +
			"the incoming branch's state must replace the folder, not merge into it")
	}
	got, _ := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if string(got) != "other branch" {
		t.Errorf("slot1.sav = %q, want %q", got, "other branch")
	}
}
