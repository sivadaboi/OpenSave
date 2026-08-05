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

	branch, err := env.mgr.CreateBranch("game1", "ng-plus")
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

// The branch has to actually diverge afterwards: play on the new branch, go
// back, and each side must hold its own state. Carrying the files over must
// not quietly make the two branches the same thing.
func TestBranchesDivergeAfterSwitching(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main progress")

	branch, err := env.mgr.CreateBranch("game1", "experiment")
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

	branch, err := env.mgr.CreateBranch("game1", "other")
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
