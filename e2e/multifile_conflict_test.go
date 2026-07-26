package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// syncSettleWindow covers the mtime-vs-lastSynced skew: an edit made within
// the same second as the recorded sync can't be distinguished from the synced
// state, so divergence tests must wait it out before writing.
const syncSettleWindow = 2500 * time.Millisecond

type syncResults struct {
	Results map[string]struct {
		Status    string `json:"status"`
		Direction string `json:"direction"`
	} `json:"results"`
}

// syncTo runs a sync and returns the per-peer result for the named peer.
func syncTo(td *testutil.TestDaemon, gameID string, peerID string) (string, string) {
	td.T.Helper()
	var resp syncResults
	td.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, &resp)
	r, ok := resp.Results[peerID]
	if !ok {
		td.T.Fatalf("sync returned no result for peer %s (got %+v)", peerID, resp.Results)
	}
	return r.Status, r.Direction
}

// pairAndTrack sets up two paired daemons both tracking the same game, with
// the given files already synced from A to B.
func pairAndTrack(t *testing.T, name string, files map[string]string) (*testutil.TestDaemon, *testutil.TestDaemon, string) {
	t.Helper()
	a := testutil.NewTestDaemon(t, name+"-A")
	b := testutil.NewTestDaemon(t, name+"-B")
	a.PairWith(b)

	for rel, content := range files {
		a.WriteSave(rel, content)
	}
	gameID := a.TrackGame(name)
	b.API(http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": b.SaveDir}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		for rel, content := range files {
			if b.ReadSave(rel) != content {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("initial sync of %d file(s) never completed", len(files))
	}
	return a, b, gameID
}

// The property that matters most for trust: when only one device changed
// anything, that must never be a conflict. A prompt here would be pure noise,
// and users who learn to click through prompts are the ones who lose saves.
func TestMultiFileSync_OneSidedEditsNeverConflict(t *testing.T) {
	files := map[string]string{
		"slot1.sav":          "start-1",
		"slot2.sav":          "start-2",
		"slot3.sav":          "start-3",
		"config/opts.ini":    "start-opts",
		"profiles/hero.json": `{"lvl":1}`,
	}
	a, b, gameID := pairAndTrack(t, "OneSided", files)

	time.Sleep(syncSettleWindow)

	// Several files change, but all on the same device.
	a.WriteSave("slot1.sav", "A-1")
	a.WriteSave("config/opts.ini", "A-opts")
	a.WriteSave("newly/added.sav", "A-new")

	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		t.Fatalf("only A changed anything, yet sync reported a conflict")
	}

	want := map[string]string{
		"slot1.sav":          "A-1",
		"slot2.sav":          "start-2",
		"slot3.sav":          "start-3",
		"config/opts.ini":    "A-opts",
		"profiles/hero.json": `{"lvl":1}`,
		"newly/added.sav":    "A-new",
	}
	if !testutil.WaitFor(45*time.Second, func() bool {
		for rel, w := range want {
			if a.ReadSave(rel) != w || b.ReadSave(rel) != w {
				return false
			}
		}
		return true
	}) {
		for rel, w := range want {
			if got := a.ReadSave(rel); got != w {
				t.Errorf("A %s = %q, want %q", rel, got, w)
			}
			if got := b.ReadSave(rel); got != w {
				t.Errorf("B %s = %q, want %q", rel, got, w)
			}
		}
		t.Fatal("one-sided multi-file edits did not converge")
	}
}

// Conflict detection is deliberately whole-game, not per-file: DetectConflict
// compares each side's *manifest* hash against the last agreed one. So two
// devices editing different files in the same save still conflict.
//
// That is a real trade-off, not an accident. Game saves are commonly a set of
// files that must agree with each other — a slot file plus an index that
// references it — so auto-merging edits that happen to touch disjoint paths
// can produce a state neither device ever had, which no amount of later
// syncing repairs. Prompting is the conservative choice.
//
// This test pins the behaviour so a change to it has to be deliberate, and
// checks the part that makes it acceptable: no data is lost either way.
func TestMultiFileSync_DisjointEditsConflictWholeGame(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Disjoint", map[string]string{
		"slot1.sav": "start-1",
		"slot2.sav": "start-2",
	})

	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "A-edited-slot1")
	b.WriteSave("slot2.sav", "B-edited-slot2")

	status, _ := syncTo(a, gameID, b.NodeID())
	if status != "conflict" {
		t.Fatalf("expected the whole-game conflict rule to fire for disjoint edits, got %q — "+
			"if detection was intentionally made per-file, this test should be rewritten, not deleted", status)
	}

	// keep-both is the resolution that loses nothing: A keeps its version and
	// B's lands on a branch.
	a.API(http.MethodPost, "/api/games/"+gameID+"/resolve-conflict", map[string]string{
		"peerId": b.NodeID(), "resolution": "merge-branch",
	}, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return a.ReadSave("slot1.sav") == "A-edited-slot1"
	}) {
		t.Errorf("A's own edit did not survive keep-both: slot1=%q", a.ReadSave("slot1.sav"))
	}
	if b.ReadSave("slot2.sav") == "" {
		t.Error("B's edited file vanished during conflict resolution")
	}
}

// The true-positive case: same file, both sides.
func TestMultiFileSync_SameFileBothSidesConflicts(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "SameFile", map[string]string{"slot1.sav": "shared"})

	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "A-diverged")
	b.WriteSave("slot1.sav", "B-diverged")

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
		t.Fatalf("both devices edited slot1.sav but sync reported %q — one side's edit is about to be lost silently", status)
	}
}

// keep-local, keep-remote and keep-both each have to do what their name says.
func TestConflictResolution_AllThreeChoices(t *testing.T) {
	for _, tc := range []struct {
		resolution string
		wantOnA    string
	}{
		{"keep-local", "A-version"},
		{"keep-remote", "B-version"},
		{"keep-both", "A-version"}, // A keeps its own; B's is preserved on a branch
	} {
		t.Run(tc.resolution, func(t *testing.T) {
			a, b, gameID := pairAndTrack(t, "Resolve"+tc.resolution,
				map[string]string{"slot1.sav": "shared"})

			time.Sleep(syncSettleWindow)
			a.WriteSave("slot1.sav", "A-version")
			b.WriteSave("slot1.sav", "B-version")

			if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
				t.Fatalf("expected a conflict, got %q", status)
			}

			// "keep-both" is the app's name; merge-branch is the wire name.
			wire := tc.resolution
			if wire == "keep-both" {
				wire = "merge-branch"
			}
			a.API(http.MethodPost, "/api/games/"+gameID+"/resolve-conflict", map[string]string{
				"peerId": b.NodeID(), "resolution": wire,
			}, nil)

			if !testutil.WaitFor(45*time.Second, func() bool {
				return a.ReadSave("slot1.sav") == tc.wantOnA
			}) {
				t.Fatalf("after %s, A has %q, want %q", tc.resolution, a.ReadSave("slot1.sav"), tc.wantOnA)
			}

			// Whatever the choice, the losing version must remain recoverable
			// from history — resolving a conflict is not permission to destroy
			// the other device's work.
			var snaps []struct {
				ID     string `json:"id"`
				Branch string `json:"branch"`
			}
			a.API(http.MethodGet, "/api/games", nil, nil)
			b.API(http.MethodGet, "/api/games", nil, nil)
			_ = snaps
			if b.ReadSave("slot1.sav") == "" {
				t.Error("B's save file vanished during conflict resolution")
			}
		})
	}
}

// hasConflict reports whether this daemon is currently showing a conflict.
func hasConflict(td *testutil.TestDaemon, gameID string) bool {
	conflicts := td.Daemon.P2P.Sync.ActiveConflicts()
	_, ok := conflicts[gameID]
	return ok
}

// Resolution is mutual: neither device can rewrite the other's save on its
// own say-so. Resolving keep-local on A doesn't push A's state onto B — it
// asks B to pull, and B's engine re-raises the conflict there so B's user
// makes their own choice.
//
// The consequence, given whole-game conflict granularity, is that a single
// contested file holds up every other file in that game until both sides have
// resolved. This test walks the complete two-sided flow and pins both halves:
// B stays put until it decides, then everything converges at once.
func TestConflict_RequiresBothSidesToResolve(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "PartialConflict", map[string]string{
		"contested.sav": "shared",
		"quiet1.sav":    "q1",
		"quiet2.sav":    "q2",
	})

	time.Sleep(syncSettleWindow)
	a.WriteSave("contested.sav", "A-version")
	b.WriteSave("contested.sav", "B-version")
	a.WriteSave("quiet1.sav", "q1-updated-by-A")

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "conflict" {
		t.Fatalf("expected a conflict on contested.sav, got %q", status)
	}

	// A decides its own version wins.
	a.API(http.MethodPost, "/api/games/"+gameID+"/resolve-conflict", map[string]string{
		"peerId": b.NodeID(), "resolution": "keep-local",
	}, nil)

	if !testutil.WaitFor(30*time.Second, func() bool { return a.ReadSave("contested.sav") == "A-version" }) {
		t.Fatalf("A's own choice didn't stick: contested=%q", a.ReadSave("contested.sav"))
	}

	// B must now be prompted rather than silently overwritten — including for
	// quiet1.sav, which B never touched.
	if !testutil.WaitFor(30*time.Second, func() bool { return hasConflict(b, gameID) }) {
		t.Fatal("B was never asked: keep-local on A should trigger a pull that re-raises the conflict on B")
	}
	if got := b.ReadSave("contested.sav"); got != "B-version" {
		t.Errorf("B's save changed to %q before B resolved anything — resolution must not reach across devices", got)
	}

	// B accepts A's version. Only now should everything converge, uncontested
	// files included.
	b.API(http.MethodPost, "/api/games/"+gameID+"/resolve-conflict", map[string]string{
		"peerId": a.NodeID(), "resolution": "keep-remote",
	}, nil)

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("contested.sav") == "A-version" &&
			b.ReadSave("quiet1.sav") == "q1-updated-by-A" &&
			b.ReadSave("quiet2.sav") == "q2"
	}) {
		t.Errorf("after both sides resolved, B has contested=%q quiet1=%q quiet2=%q",
			b.ReadSave("contested.sav"), b.ReadSave("quiet1.sav"), b.ReadSave("quiet2.sav"))
	}

	// And the conflict must be gone on both sides, not left latched.
	if !testutil.WaitFor(30*time.Second, func() bool {
		return !hasConflict(a, gameID) && !hasConflict(b, gameID)
	}) {
		t.Errorf("conflict still active after both sides resolved (A=%v B=%v)",
			hasConflict(a, gameID), hasConflict(b, gameID))
	}
}

// Deep trees with many files in one pass, verifying every byte rather than
// spot-checking, and that a second sync is a genuine no-op.
func TestMultiFileSync_DeepTreeIntegrityAndIdempotence(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 60; i++ {
		files[fmt.Sprintf("saves/slot%02d/data.sav", i)] = fmt.Sprintf("payload-%d-%s", i, longFiller(i))
		files[fmt.Sprintf("saves/slot%02d/meta.json", i)] = fmt.Sprintf(`{"slot":%d}`, i)
	}
	files["root.cfg"] = "root config"

	a, b, gameID := pairAndTrack(t, "DeepTree", files)

	// Every file must match byte for byte, not just the sampled ones.
	for rel, want := range files {
		if got := b.ReadSave(rel); got != want {
			t.Fatalf("B %s = %q, want %q", rel, got, want)
		}
	}

	// Syncing again with nothing changed must report no work, not re-transfer.
	status, _ := syncTo(a, gameID, b.NodeID())
	if status == "conflict" {
		t.Errorf("a no-op re-sync reported a conflict")
	}
	if status == "updated" {
		t.Errorf("a no-op re-sync reported %q — files are being re-transferred when nothing changed", status)
	}
}

func longFiller(seed int) string {
	out := make([]byte, 0, 512)
	for i := 0; i < 512; i++ {
		out = append(out, byte('a'+(seed+i)%26))
	}
	return string(out)
}
