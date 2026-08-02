package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

type snapshotWire struct {
	ID           string `json:"id"`
	Comment      string `json:"comment"`
	Branch       string `json:"branch"`
	IsSystemAuto bool   `json:"isSystemAuto"`
}

type gameWire struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ActiveBranch string `json:"activeBranch"`
	MaxSnapshots int    `json:"maxSnapshots"`
	Branches     map[string]struct {
		Name      string         `json:"name"`
		Snapshots []snapshotWire `json:"snapshots"`
	} `json:"branches"`
}

// game reads one game's full state back through the dashboard API.
func game(td *testutil.TestDaemon, gameID string) gameWire {
	td.T.Helper()
	var games map[string]gameWire
	td.API(http.MethodGet, "/api/games", nil, &games)
	g, ok := games[gameID]
	if !ok {
		td.T.Fatalf("game %s missing from /api/games", gameID)
	}
	return g
}

// snapshotsOn returns the snapshots recorded on a branch, oldest first.
func snapshotsOn(td *testutil.TestDaemon, gameID, branch string) []snapshotWire {
	return game(td, gameID).Branches[branch].Snapshots
}

// The point of snapshots is being able to go back. This walks the whole loop:
// take one, change the save, roll back, and confirm the bytes returned.
func TestHistory_SnapshotAndRollback(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Hist-A")

	a.WriteSave("slot1.sav", "level-1")
	a.WriteSave("inventory.dat", "sword")
	gameID := a.TrackGame("History Game")

	// Tracking takes an initial snapshot in the background.
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("tracking never produced its initial snapshot")
	}

	var snap snapshotWire
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "before the boss"}, &snap)
	if snap.ID == "" {
		t.Fatal("snapshot creation returned no id")
	}

	// Play on: both files change.
	a.WriteSave("slot1.sav", "level-9")
	a.WriteSave("inventory.dat", "cursed sword")

	a.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snap.ID}, nil)

	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.ReadSave("slot1.sav") == "level-1" && a.ReadSave("inventory.dat") == "sword"
	}) {
		t.Fatalf("rollback did not restore the save: slot1=%q inventory=%q",
			a.ReadSave("slot1.sav"), a.ReadSave("inventory.dat"))
	}

	// Rolling back is itself non-destructive: the pre-rollback state must
	// still be reachable, or "undo" would be a one-way door.
	snaps := snapshotsOn(a, gameID, "main")
	if len(snaps) < 3 {
		t.Errorf("expected a safety snapshot of the pre-rollback state, have %d snapshots", len(snaps))
	}
}

// Branches let two playthroughs coexist. Switching between them has to carry
// the save files with it, not just relabel history.
func TestHistory_BranchesKeepSeparateSaves(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Branch-A")

	a.WriteSave("slot1.sav", "main-line")
	gameID := a.TrackGame("Branch Game")
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("no initial snapshot")
	}

	a.API(http.MethodPost, "/api/games/"+gameID+"/branch", map[string]string{"name": "nightmare"}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch", map[string]string{"name": "nightmare"}, nil)

	if got := game(a, gameID).ActiveBranch; got != "nightmare" {
		t.Fatalf("active branch = %q, want nightmare", got)
	}

	// Diverge on the new branch.
	a.WriteSave("slot1.sav", "nightmare-run")
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "nightmare progress"}, nil)

	// Back to main: the original save must come back.
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch", map[string]string{"name": "main"}, nil)
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.ReadSave("slot1.sav") == "main-line"
	}) {
		t.Fatalf("switching back to main left slot1=%q, want main-line", a.ReadSave("slot1.sav"))
	}

	// And forward again: the nightmare run must not have been lost.
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch", map[string]string{"name": "nightmare"}, nil)
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.ReadSave("slot1.sav") == "nightmare-run"
	}) {
		t.Fatalf("switching back to nightmare left slot1=%q, want nightmare-run", a.ReadSave("slot1.sav"))
	}

	// main must be protected from deletion; a side branch must be removable.
	if status := a.APIStatus(http.MethodDelete, "/api/games/"+gameID+"/branch/main", nil, nil); status < 400 {
		t.Errorf("deleting the main branch returned %d — it should be refused", status)
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch", map[string]string{"name": "main"}, nil)
	a.API(http.MethodDelete, "/api/games/"+gameID+"/branch/nightmare", nil, nil)
	if _, still := game(a, gameID).Branches["nightmare"]; still {
		t.Error("deleted branch is still listed")
	}
}

// Retention has to actually bound disk use, and has to keep the newest.
func TestHistory_PruneEnforcesRetention(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Prune-A")

	a.WriteSave("slot1.sav", "v0")
	gameID := a.TrackGame("Prune Game")
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("no initial snapshot")
	}

	for i := 1; i <= 6; i++ {
		a.WriteSave("slot1.sav", fmt.Sprintf("v%d", i))
		a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
			map[string]string{"comment": fmt.Sprintf("save %d", i)}, nil)
	}
	before := len(snapshotsOn(a, gameID, "main"))
	if before < 7 {
		t.Fatalf("expected at least 7 snapshots before pruning, have %d", before)
	}

	// Cap this game at 3 and prune. These snapshots were taken through the
	// snapshot endpoint, so they are manual ones and belong to the manual
	// budget — maxSnapshots governs only the watcher's automatic snapshots,
	// which are deliberately not allowed to evict deliberate ones.
	a.API(http.MethodPatch, "/api/games/"+gameID,
		map[string]any{"maxSnapshots": 3, "maxManualSnapshots": 3}, nil)
	var pruneResp struct {
		Removed int `json:"removed"`
	}
	a.API(http.MethodPost, "/api/snapshots/prune", map[string]any{}, &pruneResp)

	after := snapshotsOn(a, gameID, "main")
	// Each kind is held to its own budget, so the total is the sum of the
	// two rather than a single number — tracking the game took an automatic
	// snapshot of its own, which the manual limit has no say over.
	manual := 0
	for _, s := range after {
		if !s.IsSystemAuto {
			manual++
		}
	}
	if manual > 3 {
		t.Errorf("after pruning to a manual limit of 3, %d manual snapshots remain", manual)
	}
	if pruneResp.Removed == 0 {
		t.Error("prune reported removing nothing despite being over the limit")
	}
	// Newest must survive — pruning the recent ones would defeat the purpose.
	if len(after) > 0 && after[len(after)-1].Comment != "save 6" {
		t.Errorf("newest surviving snapshot is %q, want %q", after[len(after)-1].Comment, "save 6")
	}
}

// Browsing inside a snapshot and pulling one file out of it, without
// rolling the whole save back.
func TestHistory_SnapshotFileListingAndSingleFileRestore(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Files-A")

	a.WriteSave("slot1.sav", "keep-me")
	a.WriteSave("nested/deep/other.sav", "also-keep")
	gameID := a.TrackGame("Files Game")
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("no initial snapshot")
	}

	var snap snapshotWire
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "reference"}, &snap)

	var listing []struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}
	a.API(http.MethodGet, "/api/games/"+gameID+"/snapshot/"+snap.ID+"/files", nil, &listing)

	found := map[string]bool{}
	for _, f := range listing {
		if !f.IsDir {
			found[f.Path] = true
		}
	}
	for _, want := range []string{"slot1.sav", "nested/deep/other.sav"} {
		if !found[want] {
			t.Errorf("snapshot listing is missing %q (got %v)", want, found)
		}
	}

	// Corrupt one file, restore just that one, leave the other alone.
	a.WriteSave("slot1.sav", "corrupted")
	a.WriteSave("nested/deep/other.sav", "deliberately changed")

	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot/"+snap.ID+"/restore-file",
		map[string]string{"relPath": "slot1.sav"}, nil)

	if !testutil.WaitFor(30*time.Second, func() bool { return a.ReadSave("slot1.sav") == "keep-me" }) {
		t.Errorf("single-file restore left slot1=%q, want keep-me", a.ReadSave("slot1.sav"))
	}
	if got := a.ReadSave("nested/deep/other.sav"); got != "deliberately changed" {
		t.Errorf("restoring one file also reverted another: other.sav=%q", got)
	}
}

// Deleting a single snapshot must remove it and free its archive, without
// disturbing the rest of the history.
func TestHistory_DeleteSingleSnapshot(t *testing.T) {
	a := testutil.NewTestDaemon(t, "SnapDel-A")

	a.WriteSave("slot1.sav", "v0")
	gameID := a.TrackGame("SnapDel Game")
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("no initial snapshot")
	}

	var doomed snapshotWire
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "doomed"}, &doomed)
	a.WriteSave("slot1.sav", "v1")
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "survivor"}, nil)

	before := len(snapshotsOn(a, gameID, "main"))
	a.API(http.MethodDelete, "/api/games/"+gameID+"/snapshot/"+doomed.ID, nil, nil)

	after := snapshotsOn(a, gameID, "main")
	if len(after) != before-1 {
		t.Errorf("snapshot count went %d -> %d, want one fewer", before, len(after))
	}
	for _, s := range after {
		if s.ID == doomed.ID {
			t.Error("deleted snapshot is still listed")
		}
	}
	var stillThere bool
	for _, s := range after {
		if s.Comment == "survivor" {
			stillThere = true
		}
	}
	if !stillThere {
		t.Error("deleting one snapshot removed a different one")
	}
}
