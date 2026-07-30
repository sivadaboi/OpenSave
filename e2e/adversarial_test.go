package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensave/opensave/testutil"
)

// snapshotOf takes a snapshot and returns its id.
func snapshotOf(td *testutil.TestDaemon, gameID, comment string) string {
	td.T.Helper()
	var snap struct {
		ID string `json:"id"`
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": comment}, &snap)
	if snap.ID == "" {
		td.T.Fatalf("snapshot of %s returned no id", gameID)
	}
	return snap.ID
}

// A corrupt snapshot must fail the restore, not consume the live save on the
// way to failing. This is the worst plausible outcome for a backup tool: you
// reach for the safety net specifically because something already went wrong,
// and a half-applied restore leaves you with neither version.
func TestAdversarial_CorruptSnapshotDoesNotDestroyTheLiveSave(t *testing.T) {
	td := testutil.NewTestDaemon(t, "CorruptSnap")

	td.WriteSave("slot1.sav", "live-and-important")
	td.WriteSave("slot2.sav", "also-important")
	gameID := td.TrackGame("Corruptible")
	snapID := snapshotOf(td, gameID, "good")

	// Find the snapshot archive and truncate it into garbage.
	var zipPath string
	root := filepath.Join(td.Daemon.Paths.HomeDir, "backups")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.Contains(filepath.Base(p), snapID) {
			zipPath = p
		}
		return nil
	})
	if zipPath == "" {
		t.Skipf("could not locate the snapshot archive under %s", root)
	}
	if err := os.WriteFile(zipPath, []byte("this is not a zip file"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := td.APIStatus(http.MethodPost,
		"/api/games/"+gameID+"/rollback", map[string]string{"snapshotId": snapID}, nil)
	if status == http.StatusOK {
		t.Error("restoring a corrupt archive reported success")
	}

	// Whatever happened, the live save must still be there and intact.
	if got := td.ReadSave("slot1.sav"); got != "live-and-important" {
		t.Errorf("slot1.sav = %q — a failed restore damaged the live save", got)
	}
	if got := td.ReadSave("slot2.sav"); got != "also-important" {
		t.Errorf("slot2.sav = %q — a failed restore damaged the live save", got)
	}
}

// Save files are named by games, not by us. Unicode, spaces, punctuation and
// mixed case all turn up in the wild, and a path bug that drops or mangles one
// of them loses data silently.
func TestAdversarial_AwkwardFilenamesSurviveASnapshotRoundTrip(t *testing.T) {
	td := testutil.NewTestDaemon(t, "AwkwardNames")

	files := map[string]string{
		"plain.sav":                 "plain",
		"with spaces.sav":           "spaces",
		"UPPER.SAV":                 "upper",
		"dots.in.name.sav":          "dots",
		"dash-and_underscore.sav":   "dashes",
		"parens(1).sav":             "parens",
		"unicode-日本語.sav":           "japanese",
		"emoji-🎮.sav":               "emoji",
		"accented-café.sav":         "accents",
		"nested/deep/inner/x.sav":   "nested",
		"'quoted'.sav":              "quotes",
		"#hash.sav":                 "hash",
		"percent%20.sav":            "percent",
		"plus+sign.sav":             "plus",
	}
	for rel, content := range files {
		td.WriteSave(rel, content)
	}

	gameID := td.TrackGame("Awkward")
	snapID := snapshotOf(td, gameID, "all the names")

	// Wreck every one of them, then restore.
	for rel := range files {
		td.WriteSave(rel, "WRECKED")
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snapID}, nil)

	for rel, want := range files {
		if got := td.ReadSave(rel); got != want {
			t.Errorf("%q = %q, want %q — this filename does not survive a round trip", rel, got, want)
		}
	}
}

// Zero-byte files are real: games create placeholder and lock files, and a
// block-hashing scheme that assumes content can drop them.
func TestAdversarial_EmptyFilesAndDirectoriesSurvive(t *testing.T) {
	td := testutil.NewTestDaemon(t, "EmptyThings")

	td.WriteSave("has-content.sav", "content")
	td.WriteSave("empty.sav", "")
	td.WriteSave("nested/also-empty.dat", "")
	if err := os.MkdirAll(filepath.Join(td.SaveDir, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	gameID := td.TrackGame("Empties")
	snapID := snapshotOf(td, gameID, "with empties")

	// Remove everything, then restore.
	if err := os.RemoveAll(td.SaveDir); err != nil {
		t.Fatal(err)
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snapID}, nil)

	if got := td.ReadSave("has-content.sav"); got != "content" {
		t.Errorf("has-content.sav = %q, want %q", got, "content")
	}
	for _, rel := range []string{"empty.sav", "nested/also-empty.dat"} {
		p := filepath.Join(td.SaveDir, filepath.FromSlash(rel))
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s was not restored: %v", rel, err)
			continue
		}
		if info.Size() != 0 {
			t.Errorf("%s restored with %d bytes, want an empty file", rel, info.Size())
		}
	}
	if info, err := os.Stat(filepath.Join(td.SaveDir, "empty-dir")); err != nil || !info.IsDir() {
		t.Errorf("the empty directory was not restored: %v", err)
	}
}

// A read-only save file is common — some games set it, and so do users who
// have been burned before. Restoring over one must either work or fail
// cleanly, never leave a partially written file.
func TestAdversarial_ReadOnlyFileRestore(t *testing.T) {
	td := testutil.NewTestDaemon(t, "ReadOnly")

	td.WriteSave("locked.sav", "original")
	gameID := td.TrackGame("ReadOnlyGame")
	snapID := snapshotOf(td, gameID, "before")

	td.WriteSave("locked.sav", "changed")
	p := filepath.Join(td.SaveDir, "locked.sav")
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })

	td.APIStatus(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snapID}, nil)

	// Either outcome is acceptable; a truncated or empty file is not.
	got := td.ReadSave("locked.sav")
	if got != "original" && got != "changed" {
		t.Errorf("locked.sav = %q — the restore left it in neither state", got)
	}
}

// Many small files is the shape of emulator saves and Unity PlayerPrefs
// trees. This is about correctness at volume, not speed: nothing may be
// dropped, duplicated or truncated.
func TestAdversarial_ManyFilesRoundTrip(t *testing.T) {
	td := testutil.NewTestDaemon(t, "ManyFiles")

	const n = 250
	want := map[string]string{}
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("slots/save_%03d.dat", i)
		content := fmt.Sprintf("content-of-file-%d", i)
		want[rel] = content
		td.WriteSave(rel, content)
	}

	gameID := td.TrackGame("ManyFilesGame")
	snapID := snapshotOf(td, gameID, "all of them")

	if err := os.RemoveAll(td.SaveDir); err != nil {
		t.Fatal(err)
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snapID}, nil)

	var missing, wrong int
	for rel, expect := range want {
		got := td.ReadSave(rel)
		switch {
		case got == "":
			missing++
		case got != expect:
			wrong++
		}
	}
	if missing > 0 || wrong > 0 {
		t.Errorf("after restoring %d files: %d missing, %d with wrong content", n, missing, wrong)
	}
}

// Rolling back to the same snapshot twice must land in the same place. A
// restore that is not idempotent means the second attempt after a scare does
// something different from the first.
func TestAdversarial_RepeatedRollbackIsIdempotent(t *testing.T) {
	td := testutil.NewTestDaemon(t, "RepeatRollback")

	td.WriteSave("a.sav", "one")
	td.WriteSave("b/c.sav", "two")
	gameID := td.TrackGame("Repeatable")
	snapID := snapshotOf(td, gameID, "base")

	td.WriteSave("a.sav", "changed")
	for i := 0; i < 3; i++ {
		td.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
			map[string]string{"snapshotId": snapID}, nil)
		if got := td.ReadSave("a.sav"); got != "one" {
			t.Fatalf("rollback %d: a.sav = %q, want %q", i+1, got, "one")
		}
		if got := td.ReadSave("b/c.sav"); got != "two" {
			t.Fatalf("rollback %d: b/c.sav = %q, want %q", i+1, got, "two")
		}
	}
}

// A file added after the snapshot is not in it. Restoring must not silently
// leave it behind masquerading as restored state — whichever policy is
// chosen, it has to be consistent, because the user is about to trust that
// the folder matches the snapshot they picked.
func TestAdversarial_RestoreHandlesFilesAddedAfterTheSnapshot(t *testing.T) {
	td := testutil.NewTestDaemon(t, "ExtraFiles")

	td.WriteSave("in-snapshot.sav", "snapshotted")
	gameID := td.TrackGame("ExtraFilesGame")
	snapID := snapshotOf(td, gameID, "before the extra file")

	td.WriteSave("added-later.sav", "not in the snapshot")
	td.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snapID}, nil)

	if got := td.ReadSave("in-snapshot.sav"); got != "snapshotted" {
		t.Errorf("the snapshotted file was not restored: %q", got)
	}

	// Whatever the policy, the file must not be half-present: either intact
	// or gone, never truncated.
	if got := td.ReadSave("added-later.sav"); got != "" && got != "not in the snapshot" {
		t.Errorf("added-later.sav = %q — left in a partial state by the restore", got)
	}
}
