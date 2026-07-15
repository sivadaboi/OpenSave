package presets

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
	// Default to Windows conventions (the fixtures use %APPDATA% layouts);
	// Linux tests override GOOS after construction.
	return &Scanner{CacheFile: filepath.Join(dir, "steam-app-cache.json"), GOOS: "windows"}
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

// TestGenerateEmbeddedIndex regenerates the embedded manifest index from
// the locally cached manifest YAML. Not a real test — run manually when
// bumping the bundled snapshot:
//
//	GEN_EMBED=1 go test ./internal/presets/ -run GenerateEmbeddedIndex
func TestGenerateEmbeddedIndex(t *testing.T) {
	if os.Getenv("GEN_EMBED") == "" {
		t.Skip("set GEN_EMBED=1 to regenerate the embedded index")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(home, ".opensave", "ludusavi-manifest.yaml")
	games := buildManifestIndex(yamlPath)
	if len(games) < 10000 {
		t.Fatalf("suspiciously small index (%d games) — refusing to embed", len(games))
	}
	raw, err := json.Marshal(games)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	out := filepath.Join("embedded", "ludusavi-index.json.gz")
	if err := os.MkdirAll("embedded", 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}
	t.Logf("embedded %d games, %d bytes compressed", len(games), buf.Len())
}

func TestEmbeddedIndexLoads(t *testing.T) {
	games := loadEmbeddedIndex()
	if len(games) < 10000 {
		t.Fatalf("embedded index has %d games, expected a full snapshot", len(games))
	}
}

func TestEntryIsSaveEntry(t *testing.T) {
	linux := []struct {
		OS    string `yaml:"os"`
		Store string `yaml:"store"`
	}{{OS: "linux"}}
	cases := []struct {
		tpl   string
		entry manifestFileEntry
		want  bool
	}{
		{"<winAppData>/X", manifestFileEntry{}, true}, // untagged counts as save
		{"<winAppData>/X", manifestFileEntry{Tags: []string{"save"}}, true},
		{"<winAppData>/X", manifestFileEntry{Tags: []string{"config"}}, false},
		{"<xdgConfig>/X", manifestFileEntry{Tags: []string{"save"}}, true},               // Linux save now kept
		{"<xdgData>/X", manifestFileEntry{Tags: []string{"save"}}, true},                 // Linux save now kept
		{"<winAppData>/X", manifestFileEntry{Tags: []string{"save"}, When: linux}, true}, // resolves under Proton
		{"<winDir>/X", manifestFileEntry{Tags: []string{"save"}}, false},                 // unresolvable everywhere
		{"<dataDrive>/X", manifestFileEntry{Tags: []string{"save"}}, false},              // unresolvable everywhere
		{"relative/path", manifestFileEntry{Tags: []string{"save"}}, false},              // no resolvable base
	}
	for i, c := range cases {
		if got := entryIsSaveEntry(c.tpl, c.entry); got != c.want {
			t.Errorf("case %d (%q): entryIsSaveEntry = %v, want %v", i, c.tpl, got, c.want)
		}
	}
}

func TestLudusavi_LinuxNativeXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	save := filepath.Join(home, ".local", "share", "Terraria", "Players")
	if err := os.MkdirAll(save, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "p1.plr"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Terraria:
  files:
    <xdgData>/Terraria/Players:
      tags: [save]
      when:
        - os: linux
`)
	sc.GOOS = "linux"
	sc.HomeDir = home
	sc.SteamRoots = []string{} // no Steam libraries in this test

	found := sc.scanLudusavi(map[string]bool{})
	if len(found) != 1 {
		t.Fatalf("expected 1 native Linux discovery, got %d: %+v", len(found), found)
	}
	if found[0].SavePath != save {
		t.Errorf("SavePath = %q, want %q", found[0].SavePath, save)
	}
}

func TestLudusavi_ProtonPrefix(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	// A Steam library with a Proton prefix for AppID 1245620 (Elden Ring).
	lib := filepath.Join(home, ".local", "share", "Steam")
	prefix := filepath.Join(lib, "steamapps", "compatdata", "1245620", "pfx", "drive_c", "users", "steamuser")
	save := filepath.Join(prefix, "AppData", "Roaming", "EldenRing", "76561198000000000")
	if err := os.MkdirAll(save, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "ER0000.sl2"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := manifestScanner(t, `
Elden Ring:
  files:
    <winAppData>/EldenRing/<storeUserId>:
      tags: [save]
      when:
        - os: windows
  steam:
    id: 1245620
`)
	sc.GOOS = "linux"
	sc.HomeDir = home
	sc.SteamRoots = []string{lib}

	found := sc.scanLudusavi(map[string]bool{})
	if len(found) != 1 {
		t.Fatalf("expected 1 Proton discovery, got %d: %+v", len(found), found)
	}
	if found[0].SavePath != save {
		t.Errorf("SavePath = %q, want %q", found[0].SavePath, save)
	}
	if found[0].AppID != "1245620" {
		t.Errorf("AppID = %q, want 1245620", found[0].AppID)
	}
}
