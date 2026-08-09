package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// A location whose folder is gone must not blank the listing. Somebody who
// moved a drive still needs to see the files they do have, and an error here
// would take the picker away entirely at the moment it is most needed.
func TestSaveFiles_MissingLocationDoesNotBreakTheListing(t *testing.T) {
	ts := startTestServer(t)
	if err := os.WriteFile(filepath.Join(ts.saveDir, "save.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Gone Loc", "savePath": ts.saveDir})

	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(cfg, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.ini"), []byte("y"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games/gone-loc/roots", map[string]string{"name": "config", "path": cfg})

	// The drive goes away.
	if err := os.RemoveAll(cfg); err != nil {
		t.Fatal(err)
	}

	list := getSaveFiles(t, ts, "gone-loc", nil)
	got := verdicts(list)
	if _, ok := got["save.dat"]; !ok {
		t.Errorf("a missing location hid the files that are still there: %v", got)
	}
	if _, ok := got["config:settings.ini"]; ok {
		t.Error("a location that no longer exists reported files")
	}
}

// A save that is a single file is listed under its own name, which is also
// what a rule naming it matches.
func TestSaveFiles_SingleFileSave(t *testing.T) {
	ts := startTestServer(t)
	only := filepath.Join(t.TempDir(), "DefaultTGOfile1.rpgsave")
	if err := os.WriteFile(only, []byte("save"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "One File", "savePath": only})

	rules := "DefaultTGOfile1.rpgsave"
	got := verdicts(getSaveFiles(t, ts, "one-file", &rules))
	if excluded, ok := got["DefaultTGOfile1.rpgsave"]; !ok {
		t.Fatalf("a single-file save was not listed: %v", got)
	} else if !excluded {
		t.Error("a rule naming the file did not match it")
	}
}

// truncated means "there is more than this". Reporting it when the listing
// happens to end exactly on the cap sends someone hunting for files that are
// all already on screen.
func TestSaveFiles_TruncatedOnlyWhenSomethingWasActuallyCut(t *testing.T) {
	ts := startTestServer(t)
	for i := 0; i < 12; i++ {
		if err := os.WriteFile(filepath.Join(ts.saveDir, "f"+strconv.Itoa(i)+".dat"), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Cap Game", "savePath": ts.saveDir})

	// Exactly on the cap, with nothing left to visit: a complete listing.
	restore := saveFileListCap
	saveFileListCap = 12
	t.Cleanup(func() { saveFileListCap = restore })

	list := getSaveFiles(t, ts, "cap-game", nil)
	if len(list.Files) != 12 {
		t.Fatalf("listed %d files, want 12", len(list.Files))
	}
	if list.Truncated {
		t.Error("a listing that ended exactly on the cap was reported as truncated, with nothing actually cut")
	}

	// One over: now something really was left out.
	saveFileListCap = 11
	over := getSaveFiles(t, ts, "cap-game", nil)
	if len(over.Files) != 11 {
		t.Errorf("listed %d files, want the cap of 11", len(over.Files))
	}
	if !over.Truncated {
		t.Error("a listing that dropped a file did not report truncated")
	}
}

// The cap is shared across locations, and running out part-way through them
// is a real truncation even if the walk that filled it went cleanly.
func TestSaveFiles_TruncatedWhenALocationIsNeverReached(t *testing.T) {
	ts := startTestServer(t)
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(ts.saveDir, "f"+strconv.Itoa(i)+".dat"), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Split Cap", "savePath": ts.saveDir})

	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(cfg, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.ini"), []byte("y"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games/split-cap/roots", map[string]string{"name": "config", "path": cfg})

	restore := saveFileListCap
	saveFileListCap = 4 // filled by the save folder alone
	t.Cleanup(func() { saveFileListCap = restore })

	list := getSaveFiles(t, ts, "split-cap", nil)
	if !list.Truncated {
		t.Error("the second location was never reached, but the listing did not say it was incomplete")
	}
}

// The picker must never be a way to read files outside the game's own
// folders. The only path input is the game record, but a rules string that
// looks like a path must not change what is walked.
func TestSaveFiles_RulesCannotRedirectTheWalk(t *testing.T) {
	ts := startTestServer(t)
	if err := os.WriteFile(filepath.Join(ts.saveDir, "save.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secrets.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Walk Game", "savePath": ts.saveDir})

	for _, probe := range []string{"../*", "/../../*", outside, "**"} {
		list := getSaveFiles(t, ts, "walk-game", &probe)
		for _, f := range list.Files {
			if f.Path == "secrets.txt" || filepath.IsAbs(f.Path) {
				t.Errorf("rules %q produced a path outside the save folder: %q", probe, f.Path)
			}
		}
	}
}

// The picker must list exactly the files that can sync — no more. The manifest
// leaves out reparse points and anything with a dot-prefixed path segment, so
// showing those here would put a row saying "syncs" next to a file that never
// does, which is the picker lying about the only thing it is for.
func TestSaveFiles_ListsOnlyWhatCanActuallySync(t *testing.T) {
	ts := startTestServer(t)
	if err := os.WriteFile(filepath.Join(ts.saveDir, "save.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	// A dot-prefixed file, and a file inside a dot-prefixed folder.
	if err := os.WriteFile(filepath.Join(ts.saveDir, ".hidden"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ts.saveDir, ".cache"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ts.saveDir, ".cache", "blob"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "windows" {
		elsewhere := t.TempDir()
		if err := os.WriteFile(filepath.Join(elsewhere, "outside.dat"), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(ts.saveDir, "link"), elsewhere).Run()
	}

	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Only Sync", "savePath": ts.saveDir})

	got := verdicts(getSaveFiles(t, ts, "only-sync", nil))
	if _, ok := got["save.dat"]; !ok {
		t.Errorf("the real save file is missing: %v", got)
	}
	for _, hidden := range []string{".hidden", ".cache/blob", "link", "link/outside.dat"} {
		if _, ok := got[hidden]; ok {
			t.Errorf("%q was offered as pickable, but it never syncs", hidden)
		}
	}
}
