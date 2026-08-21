package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type saveFileList struct {
	Files     []saveFile `json:"files"`
	Truncated bool       `json:"truncated"`
}

func getSaveFiles(t *testing.T, ts *testServer, gameID string, rulesOverride *string) saveFileList {
	t.Helper()
	u := ts.base + "/api/games/" + gameID + "/save-files"
	if rulesOverride != nil {
		u += "?rules=" + url.QueryEscape(*rulesOverride)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save-files status = %d", resp.StatusCode)
	}
	var out saveFileList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func verdicts(list saveFileList) map[string]bool {
	out := map[string]bool{}
	for _, f := range list.Files {
		key := f.Path
		if f.Location != "" {
			key = f.Location + ":" + f.Path
		}
		out[key] = f.Excluded
	}
	return out
}

// The list is what the exclusion box is edited against, so its verdicts have
// to be the sync engine's verdicts — same matcher, same relative paths.
func TestSaveFiles_ReportsWhatSyncsAndWhatDoesNot(t *testing.T) {
	ts := startTestServer(t)
	for rel, body := range map[string]string{
		"Progress.gs":     "save",
		"Config.gs":       "device specific",
		"logs/debug.log":  "noise",
		"logs/keep.log":   "noise",
		"nested/save.dat": "more save",
	} {
		full := filepath.Join(ts.saveDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Files Game", "savePath": ts.saveDir})

	// No rules yet: everything syncs.
	got := verdicts(getSaveFiles(t, ts, "files-game", nil))
	if len(got) != 5 {
		t.Fatalf("listed %d files, want 5: %v", len(got), got)
	}
	for path, excluded := range got {
		if excluded {
			t.Errorf("%s reported excluded with no rules set", path)
		}
	}

	// Saved rules are reflected.
	ts.do(t, http.MethodPatch, "/api/games/files-game", map[string]any{"syncIgnore": "Config.gs\nlogs/"})
	got = verdicts(getSaveFiles(t, ts, "files-game", nil))
	for path, want := range map[string]bool{
		"Progress.gs":     false,
		"Config.gs":       true,
		"logs/debug.log":  true,
		"logs/keep.log":   true,
		"nested/save.dat": false,
	} {
		if got[path] != want {
			t.Errorf("%s excluded = %v, want %v", path, got[path], want)
		}
	}
}

// The whole point of showing a verdict is watching it change as you type, so
// the client can ask about rules it has not saved. Requiring a save first
// would mean writing a rule to find out whether it was the rule you meant.
func TestSaveFiles_PreviewsUnsavedRules(t *testing.T) {
	ts := startTestServer(t)
	for _, rel := range []string{"Progress.gs", "Config.gs"} {
		if err := os.WriteFile(filepath.Join(ts.saveDir, rel), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Preview Game", "savePath": ts.saveDir})

	preview := "Config.gs"
	got := verdicts(getSaveFiles(t, ts, "preview-game", &preview))
	if !got["Config.gs"] || got["Progress.gs"] {
		t.Errorf("preview rules were not applied: %v", got)
	}

	// Nothing was saved by asking.
	saved := verdicts(getSaveFiles(t, ts, "preview-game", nil))
	if saved["Config.gs"] {
		t.Error("previewing a rule saved it")
	}

	// Present-but-empty means "no rules", not "fall back to the saved ones" —
	// otherwise clearing the box could not be previewed at all.
	ts.do(t, http.MethodPatch, "/api/games/preview-game", map[string]any{"syncIgnore": "Config.gs"})
	empty := ""
	cleared := verdicts(getSaveFiles(t, ts, "preview-game", &empty))
	if cleared["Config.gs"] {
		t.Error("an empty rules override fell back to the saved rules, so clearing the box cannot be previewed")
	}
}

// Extra save locations are listed too, and named, because a rule applies to
// every one of a game's folders and the list has to show the same scope.
func TestSaveFiles_CoversExtraLocations(t *testing.T) {
	ts := startTestServer(t)
	if err := os.WriteFile(filepath.Join(ts.saveDir, "save.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Loc Game", "savePath": ts.saveDir})

	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(cfg, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"settings.ini", "Config.gs"} {
		if err := os.WriteFile(filepath.Join(cfg, rel), []byte("y"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	resp, _ := ts.do(t, http.MethodPost, "/api/games/loc-game/roots", map[string]string{"name": "config", "path": cfg})
	if resp.StatusCode >= 400 {
		t.Fatalf("adding a location failed with %d", resp.StatusCode)
	}

	rules := "Config.gs"
	list := getSaveFiles(t, ts, "loc-game", &rules)
	got := verdicts(list)
	if _, ok := got["save.dat"]; !ok {
		t.Error("the main save folder is missing from the listing")
	}
	if _, ok := got["config:settings.ini"]; !ok {
		t.Errorf("the extra location is missing from the listing: %v", got)
	}
	if !got["config:Config.gs"] {
		t.Error("a rule must apply inside an extra location, and the listing must say so")
	}
	if got["config:settings.ini"] {
		t.Error("a file the rule does not name was reported excluded")
	}

	// Excluded rows come first: the reason to open the list is to see what a
	// rule is catching, and hunting for it among hundreds of rows is the thing
	// being avoided.
	if !list.Files[0].Excluded {
		t.Errorf("excluded files should sort first, got %+v", list.Files[0])
	}
}

func TestSaveFiles_UnknownGameIs404(t *testing.T) {
	ts := startTestServer(t)
	resp, err := http.Get(ts.base + "/api/games/nope/save-files")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
