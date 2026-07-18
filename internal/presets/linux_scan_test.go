package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linuxScanner builds a Scanner in Linux mode rooted at a temp home, with
// no network resolver and no real Steam roots unless the test adds them.
func linuxScanner(t *testing.T, home string) *Scanner {
	t.Helper()
	// Neutralize the host's XDG env so ~/.config and ~/.local/share resolve
	// under the test home (CI runs on Linux with these set).
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	return &Scanner{
		CacheFile:          filepath.Join(t.TempDir(), "cache.json"),
		GOOS:               "linux",
		HomeDir:            home,
		SteamRoots:         []string{},
		SteamUserdataPaths: []string{},
		LocalLowDir:        filepath.Join(t.TempDir(), "nolocallow"),
	}
}

func TestResolveLinuxPath(t *testing.T) {
	home := "/home/deck"
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	cases := map[string]string{
		"~/.config/retroarch/saves":  "/home/deck/.config/retroarch/saves",
		"~/.local/share/dolphin-emu": "/home/deck/.local/share/dolphin-emu",
		"~/.factorio":                "/home/deck/.factorio",
		"~":                          "/home/deck",
	}
	for in, want := range cases {
		if got := filepath.ToSlash(resolveLinuxPath(in, home)); got != want {
			t.Errorf("resolveLinuxPath(%q) = %q, want %q", in, got, want)
		}
	}
	// XDG override is honored.
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")
	if got := filepath.ToSlash(resolveLinuxPath("~/.config/retroarch", home)); got != "/custom/cfg/retroarch" {
		t.Errorf("XDG_CONFIG_HOME override = %q", got)
	}
}

func TestScan_LinuxEmulatorPaths(t *testing.T) {
	home := t.TempDir()
	// RetroArch (native) and PCSX2 (native), plus a Flatpak Dolphin.
	mustMkFile(t, filepath.Join(home, ".config", "retroarch", "saves", "game.srm"))
	mustMkFile(t, filepath.Join(home, ".config", "PCSX2", "memcards", "Mcd001.ps2"))
	mustMkFile(t, filepath.Join(home, ".var", "app", "org.DolphinEmu.dolphin-emu", "data", "dolphin-emu", "GC", "card.raw"))

	sc := linuxScanner(t, home)
	got := sc.Scan(nil)

	want := map[string]bool{
		filepath.Join(home, ".config", "retroarch", "saves"):                                    false,
		filepath.Join(home, ".config", "PCSX2", "memcards"):                                     false,
		filepath.Join(home, ".var", "app", "org.DolphinEmu.dolphin-emu", "data", "dolphin-emu"): false,
	}
	for _, d := range got {
		if _, ok := want[d.SavePath]; ok {
			want[d.SavePath] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("expected emulator save %q to be detected; got %d results", path, len(got))
		}
	}
}

func TestScan_ProtonCompatdata(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, ".local", "share", "Steam")

	// Two Proton prefixes; one game named via an appmanifest.
	prefixA := filepath.Join(lib, "steamapps", "compatdata", "374320", "pfx", "drive_c", "users", "steamuser")
	mustMkFile(t, filepath.Join(prefixA, "AppData", "Roaming", "DarkSoulsIII", "save.sl2"))
	mustMkFile(t, filepath.Join(prefixA, "Documents", "My Games", "DarkSouls3", "cfg.ini"))
	// Vendor junk that must be skipped.
	mustMkFile(t, filepath.Join(prefixA, "AppData", "Roaming", "Microsoft", "stuff.dat"))

	prefixB := filepath.Join(lib, "steamapps", "compatdata", "999999", "pfx", "drive_c", "users", "steamuser")
	mustMkFile(t, filepath.Join(prefixB, "AppData", "Local", "MysteryGame", "slot0"))

	// appmanifest names AppID 374320.
	acf := filepath.Join(lib, "steamapps", "appmanifest_374320.acf")
	if err := os.WriteFile(acf, []byte(`"AppState"{"appid""374320""name""DARK SOULS III""installdir""DARK SOULS III"}`), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := &Scanner{
		CacheFile:          filepath.Join(t.TempDir(), "cache.json"),
		GOOS:               "linux",
		HomeDir:            home,
		SteamRoots:         []string{lib},
		SteamUserdataPaths: []string{},
		LocalLowDir:        filepath.Join(t.TempDir(), "nolocallow"),
	}
	got := sc.Scan(nil)

	var dsSave, mysterySave, microsoftLeak bool
	var dsNamed bool
	for _, d := range got {
		switch d.SavePath {
		case filepath.Join(prefixA, "AppData", "Roaming", "DarkSoulsIII"):
			dsSave = true
			if d.AppID == "374320" {
				dsNamed = true
			}
		case filepath.Join(prefixB, "AppData", "Local", "MysteryGame"):
			mysterySave = true
		case filepath.Join(prefixA, "AppData", "Roaming", "Microsoft"):
			microsoftLeak = true
		}
	}
	if !dsSave {
		t.Error("Dark Souls III Proton save not detected")
	}
	if !dsNamed {
		t.Error("Proton save should carry its Steam AppID for cover art")
	}
	if !mysterySave {
		t.Error("unnamed Proton game save not detected")
	}
	if microsoftLeak {
		t.Error("Microsoft vendor folder should be skipped, not offered as a save")
	}
}

// TestProtonCoarseScanDefersToManifestHits guards the "38 identical
// tiles" bug: a busy prefix's AppData/Documents vendor folders each
// became a discovery named after the prefix's game, and resolveNames
// then erased the "(subfolder)" qualifiers that told them apart.
func TestProtonCoarseScanDefersToManifestHits(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	lib := filepath.Join(home, ".local", "share", "Steam")
	steamuser := filepath.Join(lib, "steamapps", "compatdata", "2161700", "pfx", "drive_c", "users", "steamuser")

	// The real save (manifest will pinpoint it) plus prefix junk.
	realSave := filepath.Join(steamuser, "AppData", "Roaming", "SEGA", "P3R", "Steam")
	junk := [][]string{
		{"AppData", "Roaming", "CRIWARE"},          // vendor-skip list
		{"AppData", "Local", "Temp"},               // vendor-skip list
		{"Documents", "SomeUnknownVendor"},         // legit coarse candidate
	}
	for _, dir := range append([][]string{{"AppData", "Roaming", "SEGA", "P3R", "Steam"}}, junk...) {
		p := filepath.Join(append([]string{steamuser}, dir...)...)
		if err := os.MkdirAll(p, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "data.bin"), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	_ = realSave

	sc := manifestScanner(t, `
Persona 3 Reload:
  files:
    <winAppData>/SEGA/P3R/Steam:
      tags: [save]
      when:
        - os: windows
  steam:
    id: 2161700
`)
	sc.GOOS = "linux"
	sc.HomeDir = home
	sc.SteamRoots = []string{lib}

	// Mirror Scan()'s order: precise manifest pass first…
	seen := map[string]bool{}
	manifestHits := sc.scanLudusavi(seen)
	if len(manifestHits) != 1 {
		t.Fatalf("expected 1 manifest hit, got %d: %+v", len(manifestHits), manifestHits)
	}
	for _, d := range manifestHits {
		seen[d.SavePath] = true
	}

	// …then the coarse prefix listing, which must not re-offer the
	// SEGA parent (a precise hit lives inside it) nor the vendor junk.
	coarse := sc.scanProtonCompat(sc.steamLibraryPaths(), seen, map[string]string{"2161700": "Persona 3 Reload"})
	if len(coarse) != 1 {
		t.Fatalf("expected only the unknown-vendor coarse hit, got %d: %+v", len(coarse), coarse)
	}
	if got := coarse[0].Name; got != "Persona 3 Reload (SomeUnknownVendor)" {
		t.Errorf("coarse name = %q, want the qualified form", got)
	}
}

// TestRenameKeepingSuffix guards resolveNames against collapsing
// qualified names into identical tiles.
func TestRenameKeepingSuffix(t *testing.T) {
	cases := []struct{ old, base, want string }{
		{"2161700 (SEGA)", "Persona 3 Reload", "Persona 3 Reload (SEGA)"},
		{"Persona 3 Reload (CRIWARE)", "Persona 3 Reload", "Persona 3 Reload (CRIWARE)"},
		{"plain-name", "Resolved Title", "Resolved Title"},
		{"Dir (Epic/Unreal Save)", "Real Game", "Real Game (Epic/Unreal Save)"},
	}
	for _, c := range cases {
		if got := renameKeepingSuffix(c.old, c.base); got != c.want {
			t.Errorf("renameKeepingSuffix(%q, %q) = %q, want %q", c.old, c.base, got, c.want)
		}
	}
}

// TestEmuDeckDetection: EmuDeck routes every emulator's saves into one
// Emulation/saves tree (internal or SD card) — each emulator subfolder
// must surface as its own tracked candidate. Reported missing by both a
// Steam Deck tester and a Reddit user.
func TestEmuDeckDetection(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	for _, emu := range []string{"retroarch", "dolphin"} {
		p := filepath.Join(home, "Emulation", "saves", emu)
		if err := os.MkdirAll(p, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "save.srm"), []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}

	sc := &Scanner{CacheFile: filepath.Join(t.TempDir(), "cache.json"), GOOS: "linux", HomeDir: home}
	found := sc.Scan(nil)

	got := map[string]string{}
	for _, d := range found {
		if strings.HasPrefix(d.ID, "emudeck-") {
			got[d.ID] = d.Name
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 EmuDeck discoveries, got %d: %v", len(got), got)
	}
	if got["emudeck-retroarch"] != "EmuDeck (retroarch)" {
		t.Errorf("retroarch entry = %q", got["emudeck-retroarch"])
	}
	if got["emudeck-dolphin"] != "EmuDeck (dolphin)" {
		t.Errorf("dolphin entry = %q", got["emudeck-dolphin"])
	}
}

// TestPresetGlobPaths: SD-card style wildcard locations resolve through
// filepath.Glob.
func TestPresetGlobPaths(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "run-media", "mmcblk0p1", "Emulation", "saves")
	if err := os.MkdirAll(target, 0o777); err != nil {
		t.Fatal(err)
	}

	p := preset{ID: "glob-test", Name: "Glob", Type: "emulator", IsWrapper: true,
		LinuxPath: []string{"~/run-media/*/Emulation/saves"}}
	sc := &Scanner{GOOS: "linux", HomeDir: home}

	paths := p.resolvedPaths(sc)
	if len(paths) != 1 || paths[0] != target {
		t.Fatalf("resolvedPaths = %v, want [%s]", paths, target)
	}
}
