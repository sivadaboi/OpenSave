package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// mkdirOnly creates an empty directory inside a device's save folder without
// putting anything in it — the state a game leaves behind for screenshots,
// crash dumps or DLC it has not written to yet.
func mkdirOnly(td *testutil.TestDaemon, rel string) {
	td.T.Helper()
	if err := os.MkdirAll(filepath.Join(td.SaveDir, filepath.FromSlash(rel)), 0o755); err != nil {
		td.T.Fatalf("mkdir %s: %v", rel, err)
	}
}

// A save's fingerprint covers its folders as well as its files, but every
// part of the app that explains a conflict reads only the files. So an empty
// folder on one device was enough to make two identical saves look diverged:
// a conflict was raised, and the dialog then had no differing file to name
// and drew both sides with the same file count, size and timestamp. Users
// were asked to choose between two versions shown as identical, on games
// nobody had touched on the other device.
//
// A folder on one side only is not a conflict. It is something the ordinary
// sync creates on the other side — which the conflict was preventing.
func TestDirOnlyDifferenceIsNotAConflict(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "DirOnly", map[string]string{"slot1.sav": "shared"})

	// Both sides must move off the recorded merge-base for this to be the
	// reported bug. A folder added on one side only already resolves cleanly
	// — the base still matches the other side — so a test that adds one
	// folder passes with or without the fix and proves nothing.
	time.Sleep(syncSettleWindow)
	mkdirOnly(a, "screenshots")
	mkdirOnly(b, "crashdumps")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		t.Fatalf("an empty folder on one device was reported as a save conflict (status %q) — "+
			"no file differs, so the conflict dialog has nothing to show and both sides read identical", status)
	}

	// And the folder should actually arrive, since that is what the sync was
	// supposed to be doing all along — the conflict was blocking it.
	if !testutil.WaitFor(45*time.Second, func() bool {
		info, err := os.Stat(filepath.Join(b.SaveDir, "screenshots"))
		return err == nil && info.IsDir()
	}) {
		t.Error("the folder never propagated to the other device")
	}

	// The file that was already in sync must be untouched by any of this.
	if got := b.ReadSave("slot1.sav"); got != "shared" {
		t.Errorf("slot1.sav = %q, want %q — a directory sync disturbed file content", got, "shared")
	}
}

// The guard on the fix: carving directories out of conflict detection must
// not let a genuine both-sides content divergence through. A folder appearing
// on one side at the same time as real diverging edits is still a conflict.
func TestDirDifferenceAlongsideRealConflictStillConflicts(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "DirPlusEdit", map[string]string{"slot1.sav": "shared"})

	time.Sleep(syncSettleWindow)
	mkdirOnly(a, "screenshots")
	a.WriteSave("slot1.sav", "A-diverged")
	b.WriteSave("slot1.sav", "B-diverged")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status != "conflict" {
		t.Fatalf("both devices edited slot1.sav but sync reported %q — the directory carve-out "+
			"is swallowing a real conflict and one side's edit is about to be lost", status)
	}

	// The dialog must be able to account for the whole divergence, folder
	// included, rather than listing the file half and leaving the rest
	// unexplained.
	var status0 struct {
		Conflicts map[string]struct {
			DiffTotal int `json:"diffTotal"`
			DiffFiles []struct {
				Path   string `json:"path"`
				Status string `json:"status"`
			} `json:"diffFiles"`
		} `json:"conflicts"`
	}
	a.API(http.MethodGet, "/api/status", nil, &status0)
	c, ok := status0.Conflicts[gameID]
	if !ok {
		t.Fatalf("no active conflict recorded for %s", gameID)
	}
	if c.DiffTotal == 0 {
		t.Error("a conflict was raised with nothing listed as differing — this is what users saw as an empty dialog")
	}
	var sawDir bool
	for _, d := range c.DiffFiles {
		if d.Path == "screenshots/" && d.Status == "only-local" {
			sawDir = true
		}
	}
	if !sawDir {
		t.Errorf("the folder that differs was left out of the conflict summary: %+v", c.DiffFiles)
	}
}
