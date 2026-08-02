package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// conflictView is what one device believes about a conflict: which side each
// difference is on, from that device's own point of view.
type conflictView struct {
	Peer struct {
		Name string `json:"name"`
	} `json:"peer"`
	LocalStats struct {
		Files         int   `json:"files"`
		TotalBytes    int64 `json:"totalBytes"`
		LatestMtimeMs int64 `json:"latestMtimeMs"`
	} `json:"localStats"`
	RemoteStats struct {
		Files         int   `json:"files"`
		TotalBytes    int64 `json:"totalBytes"`
		LatestMtimeMs int64 `json:"latestMtimeMs"`
	} `json:"remoteStats"`
	DiffTotal int `json:"diffTotal"`
	DiffFiles []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	} `json:"diffFiles"`
}

func conflictOn(td *testutil.TestDaemon, gameID string) (conflictView, bool) {
	td.T.Helper()
	var status struct {
		Conflicts map[string]conflictView `json:"conflicts"`
	}
	td.API(http.MethodGet, "/api/status", nil, &status)
	c, ok := status.Conflicts[gameID]
	return c, ok
}

// The scenario as reported: two devices in sync, one person adds a file, and
// both ends raise a conflict — each claiming the *other* added it.
//
// Adding a file on one device while the other sits untouched is the single
// most ordinary thing this software has to handle. If that alone produces a
// conflict, sync is unusable regardless of what else works.
func TestReported_AddingOneFileOnOneDeviceMustNotConflict(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "AddOneFile", map[string]string{
		"existing.sav": "already here",
	})

	// A adds a file. B is not touched at all.
	time.Sleep(syncSettleWindow)
	a.WriteSave("newfile.sav", "added by A")

	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Fatalf("adding one file on one device raised a conflict.\n"+
			"  A sees: local %d files/%d B, remote %d files/%d B, %d differing\n"+
			"  diff: %+v",
			ca.LocalStats.Files, ca.LocalStats.TotalBytes,
			ca.RemoteStats.Files, ca.RemoteStats.TotalBytes,
			ca.DiffTotal, ca.DiffFiles)
	}

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("newfile.sav") == "added by A"
	}) {
		t.Fatal("the added file never reached the peer")
	}

	// And the pair must settle: no conflict on either side afterwards.
	time.Sleep(syncSettleWindow)
	syncTo(a, gameID, b.NodeID())
	if c, ok := conflictOn(a, gameID); ok {
		t.Errorf("A holds a conflict after a one-sided add settled: %+v", c.DiffFiles)
	}
	if c, ok := conflictOn(b, gameID); ok {
		t.Errorf("B holds a conflict after a one-sided add settled: %+v", c.DiffFiles)
	}
}

// The other half of the report: when a conflict IS raised, each device must
// describe it from its own side. Being told "the other person added this"
// about a file you added yourself makes the choice impossible to reason about
// — you would pick the wrong side believing you were keeping your own work.
func TestReported_EachSideDescribesTheConflictFromItsOwnPointOfView(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "SideView", map[string]string{"slot1.sav": "shared"})

	// A genuine two-sided divergence: each device adds a differently-named
	// file, which is the shape that actually produces a conflict.
	time.Sleep(syncSettleWindow)
	a.WriteSave("from-a.sav", "a's file")
	b.WriteSave("from-b.sav", "b's file")

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
		t.Skipf("no conflict raised (%q) — this test is about how one is described", status)
	}

	ca, okA := conflictOn(a, gameID)
	if !okA {
		t.Fatal("A reported a conflict but holds no record of it")
	}

	// On A, A's own file is local and B's is remote. Reversed would be the
	// reported bug.
	for _, d := range ca.DiffFiles {
		switch d.Path {
		case "from-a.sav":
			if d.Status != "only-local" {
				t.Errorf("on A, the file A created is reported as %q — A is being told it came from %s",
					d.Status, ca.Peer.Name)
			}
		case "from-b.sav":
			if d.Status != "only-remote" {
				t.Errorf("on A, the file %s created is reported as %q", ca.Peer.Name, d.Status)
			}
		}
	}

	// Whatever the two sides hold, they must not be described identically:
	// if both say "the other one added it", one of them is wrong.
	if cb, okB := conflictOn(b, gameID); okB {
		for _, d := range cb.DiffFiles {
			switch d.Path {
			case "from-b.sav":
				if d.Status != "only-local" {
					t.Errorf("on B, the file B created is reported as %q — both devices blaming each other is the reported bug", d.Status)
				}
			case "from-a.sav":
				if d.Status != "only-remote" {
					t.Errorf("on B, the file %s created is reported as %q", cb.Peer.Name, d.Status)
				}
			}
		}
	}
}

// The original screenshot showed both sides as "1 files 0 B" with the same
// last-change time to the second — a prompt asking which of two identical
// things to keep. Whatever raises a conflict, the dialog must be able to name
// something that actually differs, or there is nothing to decide on.
func TestReported_AConflictMustAlwaysHaveSomethingToShow(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Showable", map[string]string{"slot1.sav": "shared"})

	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "a-version")
	b.WriteSave("slot1.sav", "b-version")

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
		t.Fatalf("both devices edited the same file but sync reported %q", status)
	}

	c, ok := conflictOn(a, gameID)
	if !ok {
		t.Fatal("no conflict recorded")
	}
	if c.DiffTotal == 0 || len(c.DiffFiles) == 0 {
		t.Errorf("a conflict was raised with nothing listed as differing — "+
			"this is the dialog that showed two identical sides: local %d files/%d B, remote %d files/%d B",
			c.LocalStats.Files, c.LocalStats.TotalBytes, c.RemoteStats.Files, c.RemoteStats.TotalBytes)
	}
	// Sizes must be real: the screenshot showed 0 B for a save that was not empty.
	if c.LocalStats.Files > 0 && c.LocalStats.TotalBytes == 0 {
		t.Errorf("local side reports %d files totalling 0 bytes", c.LocalStats.Files)
	}
	if c.RemoteStats.Files > 0 && c.RemoteStats.TotalBytes == 0 {
		t.Errorf("remote side reports %d files totalling 0 bytes", c.RemoteStats.Files)
	}
}
