package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// The data-loss shape this code has produced before, now for manual
// snapshots: restoring the OLDEST one while sitting exactly at the limit.
//
// Restore takes a safety snapshot first, and that snapshot triggers pruning.
// The original bug was that pruning then deleted the archive of the very
// snapshot being restored, before it had been extracted — the rollback failed
// and the save was gone. The safety snapshot is an automatic one, so under
// separate budgets it must spend the automatic allowance and leave the manual
// target alone.
func TestRestoreOldestManualSnapshotAtTheManualLimit(t *testing.T) {
	env := setup(t)
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	game.MaxSnapshots = 1 // a tight automatic budget, so the safety snapshot prunes
	game.MaxManualSnapshots = 3
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	var manual []store.Snapshot
	for i := 0; i < 3; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "v"+string(rune('0'+i)))
		s, err := env.mgr.Create("game1", "checkpoint", false)
		if err != nil {
			t.Fatal(err)
		}
		manual = append(manual, s)
	}
	oldest := manual[0] // holds "v0"

	writeSave(t, env.saveDir, "slot1.sav", "current work")
	if _, err := env.mgr.Restore("game1", oldest.ID); err != nil {
		t.Fatalf("restoring the oldest manual snapshot at the manual limit failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v0" {
		t.Errorf("restored content = %q, want %q", got, "v0")
	}
}

// A manual limit of 1 is the tightest setting that still keeps anything. The
// snapshot just taken must be the survivor — pruning the newest would mean
// the button reports success and leaves nothing behind.
func TestManualLimitOfOneKeepsTheNewest(t *testing.T) {
	env := setup(t)
	game, _ := env.store.GetGame("game1")
	game.MaxManualSnapshots = 1
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	var last store.Snapshot
	for i := 0; i < 4; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "v"+string(rune('0'+i)))
		s, err := env.mgr.Create("game1", "checkpoint", false)
		if err != nil {
			t.Fatal(err)
		}
		last = s
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	manual := []store.Snapshot{}
	for _, s := range snaps {
		if !s.IsSystemAuto {
			manual = append(manual, s)
		}
	}
	if len(manual) != 1 {
		t.Fatalf("manual snapshots kept = %d, want 1", len(manual))
	}
	if manual[0].ID != last.ID {
		t.Errorf("kept %q, want the newest %q", manual[0].ID, last.ID)
	}
	if _, err := os.Stat(manual[0].ZipPath); err != nil {
		t.Errorf("the surviving snapshot has no archive on disk: %v", err)
	}
}

// Every kept snapshot must still have its archive, and every pruned one must
// have had its archive removed. A row without a zip is a snapshot that cannot
// be restored; a zip without a row is disk that is never reclaimed.
func TestPruningLeavesNoOrphansEitherWay(t *testing.T) {
	env := setup(t)
	game, _ := env.store.GetGame("game1")
	game.MaxSnapshots = 2
	game.MaxManualSnapshots = 2
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	var all []store.Snapshot
	for i := 0; i < 5; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "auto"+string(rune('0'+i)))
		s, err := env.mgr.Create("game1", "", true)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, s)
		writeSave(t, env.saveDir, "slot1.sav", "manual"+string(rune('0'+i)))
		m, err := env.mgr.Create("game1", "checkpoint", false)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, m)
	}

	kept, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	keptIDs := map[string]bool{}
	for _, s := range kept {
		keptIDs[s.ID] = true
		if _, err := os.Stat(s.ZipPath); err != nil {
			t.Errorf("kept snapshot %s has no archive: %v", s.ID, err)
		}
	}
	for _, s := range all {
		if keptIDs[s.ID] {
			continue
		}
		if _, err := os.Stat(s.ZipPath); !os.IsNotExist(err) {
			t.Errorf("pruned snapshot %s left its archive behind at %s", s.ID, s.ZipPath)
		}
	}
	if len(kept) != 4 { // 2 automatic + 2 manual
		t.Errorf("kept %d snapshots, want 4 (2 per kind)", len(kept))
	}
}

// Deleting a branch must take its snapshots of BOTH kinds with it. Manual
// snapshots being exempt from retention must not make them exempt from an
// explicit deletion — that would leak disk with no way to reclaim it.
func TestDeletingABranchRemovesManualSnapshotsToo(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main state")

	branch, err := env.mgr.CreateBranch("game1", "side", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", branch); err != nil {
		t.Fatal(err)
	}
	var made []store.Snapshot
	for i := 0; i < 3; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "side"+string(rune('0'+i)))
		s, err := env.mgr.Create("game1", "manual on side", false)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, s)
	}

	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	env.mgr.DeleteBranch("game1", branch)

	for _, s := range made {
		if _, err := env.store.GetSnapshot(s.ID); err == nil {
			t.Errorf("manual snapshot %s survived deletion of its branch", s.ID)
		}
		if _, err := os.Stat(s.ZipPath); !os.IsNotExist(err) {
			t.Errorf("manual snapshot %s left its archive behind after branch deletion", s.ID)
		}
	}
}

// A negative limit is not a valid budget. It must behave like "unlimited"
// rather than pruning everything, because the alternative silently deletes a
// user's whole history the moment a bad value reaches the field.
func TestNegativeLimitsKeepEverything(t *testing.T) {
	env := setup(t)
	game, _ := env.store.GetGame("game1")
	game.MaxSnapshots = -5
	game.MaxManualSnapshots = -1
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "v"+string(rune('0'+i)))
		if _, err := env.mgr.Create("game1", "manual", false); err != nil {
			t.Fatal(err)
		}
		writeSave(t, env.saveDir, "slot1.sav", "a"+string(rune('0'+i)))
		if _, err := env.mgr.Create("game1", "", true); err != nil {
			t.Fatal(err)
		}
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 8 {
		t.Errorf("with negative limits %d snapshots remain, want all 8 kept", len(snaps))
	}
}
