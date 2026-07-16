package presets

import (
	"os"
	"path/filepath"
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
