package presets

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifest drops a fixture manifest where the scanner expects it and
// returns a Scanner wired to that directory (no network).
func manifestScanner(t *testing.T, yamlBody string) *Scanner {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ludusavi-manifest.yaml"), []byte(yamlBody), 0o666); err != nil {
		t.Fatal(err)
	}
	return &Scanner{CacheFile: filepath.Join(dir, "steam-app-cache.json")}
}

func TestLudusavi_DetectsExistingSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	appdata := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("APPDATA", appdata)

	saveDir := filepath.Join(appdata, "MoonStudio", "HollowGame")
	if err := os.MkdirAll(saveDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "slot0.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Hollow Game:
  files:
    <winAppData>/MoonStudio/HollowGame:
      tags: [save]
      when:
        - os: windows
  steam:
    id: 4242
Absent Game:
  files:
    <winAppData>/Nobody/Nothing:
      tags: [save]
`)

	found := sc.scanLudusavi(map[string]bool{})
	if len(found) != 1 {
		t.Fatalf("expected 1 discovery, got %d: %+v", len(found), found)
	}
	d := found[0]
	if d.Name != "Hollow Game" || d.AppID != "4242" || d.Type != "game" {
		t.Errorf("unexpected discovery: %+v", d)
	}
	if d.SavePath != saveDir {
		t.Errorf("SavePath = %q, want %q", d.SavePath, saveDir)
	}
}

func TestLudusavi_SkipsConfigOnlyAndBlockedRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	appdata := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("APPDATA", appdata)

	cfgDir := filepath.Join(appdata, "SomeGame")
	if err := os.MkdirAll(cfgDir, 0o777); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docs, 0o777); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Config Game:
  files:
    <winAppData>/SomeGame:
      tags: [config]
Greedy Game:
  files:
    <winDocuments>:
      tags: [save]
`)

	if found := sc.scanLudusavi(map[string]bool{}); len(found) != 0 {
		t.Errorf("expected no discoveries, got %+v", found)
	}
}

func TestLudusavi_GlobAndFileToParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	saves := filepath.Join(home, "Saved Games", "GlobGame", "profiles")
	if err := os.MkdirAll(saves, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saves, "slot1.sav"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Glob Game:
  files:
    <home>/Saved Games/GlobGame/profiles/slot*.sav:
      tags: [save]
`)

	found := sc.scanLudusavi(map[string]bool{})
	if len(found) != 1 {
		t.Fatalf("expected 1 discovery, got %d: %+v", len(found), found)
	}
	if found[0].SavePath != saves {
		t.Errorf("SavePath = %q, want %q (file should map to parent dir)", found[0].SavePath, saves)
	}
}

func TestLudusavi_DedupesAgainstSeen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	appdata := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("APPDATA", appdata)

	saveDir := filepath.Join(appdata, "Dup", "Game")
	if err := os.MkdirAll(saveDir, 0o777); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Dup Game:
  files:
    <winAppData>/Dup/Game:
      tags: [save]
`)

	abs, _ := filepath.Abs(saveDir)
	if found := sc.scanLudusavi(map[string]bool{abs: true}); len(found) != 0 {
		t.Errorf("expected dedupe against seen, got %+v", found)
	}
}

func TestLudusavi_MissingManifestIsSilent(t *testing.T) {
	sc := &Scanner{CacheFile: filepath.Join(t.TempDir(), "cache.json")}
	if found := sc.scanLudusavi(map[string]bool{}); found != nil {
		t.Errorf("expected nil without a manifest, got %+v", found)
	}
}

func TestEntryIsWindowsSave(t *testing.T) {
	cases := []struct {
		tpl   string
		entry manifestFileEntry
		want  bool
	}{
		{"<winAppData>/X", manifestFileEntry{}, true}, // untagged counts as save
		{"<winAppData>/X", manifestFileEntry{Tags: []string{"save"}}, true},
		{"<winAppData>/X", manifestFileEntry{Tags: []string{"config"}}, false},
		{"<xdgConfig>/X", manifestFileEntry{Tags: []string{"save"}}, false},
		{"<winAppData>/X", manifestFileEntry{
			Tags: []string{"save"},
			When: []struct {
				OS    string `yaml:"os"`
				Store string `yaml:"store"`
			}{{OS: "linux"}},
		}, false},
	}
	for i, c := range cases {
		if got := entryIsWindowsSave(c.tpl, c.entry); got != c.want {
			t.Errorf("case %d: entryIsWindowsSave = %v, want %v", i, got, c.want)
		}
	}
}
