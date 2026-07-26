package presets

import (
	"path/filepath"
	"strings"
	"testing"
)

// mkPrefixSave builds a Wine prefix containing one save folder.
func mkPrefixSave(t *testing.T, prefix, user, root, game string) string {
	t.Helper()
	dir := filepath.Join(prefix, "drive_c", "users", user, root, game)
	mustMkFile(t, filepath.Join(dir, "save.dat"))
	return dir
}

// TestScanWinePrefixes_HeroicLutrisBottles is the reported Steam Deck gap:
// a game installed outside Steam keeps its saves in that launcher's own Wine
// prefix, which the Steam-only compatdata scan never looked inside.
func TestScanWinePrefixes_HeroicLutrisBottles(t *testing.T) {
	home := t.TempDir()

	// Heroic (native layout), prefix named after the game.
	heroic := filepath.Join(home, "Games", "Heroic", "Prefixes", "default", "Cracked Adventure")
	mkPrefixSave(t, heroic, "deck", filepath.Join("AppData", "Roaming"), "CrackedAdventure")

	// Bottles (Flatpak layout), and a prefix user that is NOT "steamuser" —
	// the hardcoded name is why these yielded nothing.
	bottles := filepath.Join(home, ".var", "app", "com.usebottles.bottles",
		"data", "bottles", "bottles", "GameBottle")
	mkPrefixSave(t, bottles, "myuser", filepath.Join("Documents", "My Games"), "BottledGame")

	// Lutris.
	lutris := filepath.Join(home, ".local", "share", "lutris", "prefixes", "SomeGame")
	mkPrefixSave(t, lutris, "deck", "Saved Games", "LutrisGame")

	// A bare ~/.wine is itself a prefix, not a parent of prefixes.
	wine := filepath.Join(home, ".wine")
	mkPrefixSave(t, wine, "deck", filepath.Join("AppData", "Local"), "PlainWineGame")

	sc := &Scanner{GOOS: "linux", HomeDir: home}
	found := sc.scanWinePrefixes(map[string]bool{})

	wanted := map[string]string{
		"CrackedAdventure": "Heroic",
		"BottledGame":      "Bottles",
		"LutrisGame":       "Lutris",
		"PlainWineGame":    "Wine",
	}
	for game, launcher := range wanted {
		var hit *DiscoveredSave
		for i := range found {
			if strings.Contains(found[i].SavePath, game) {
				hit = &found[i]
				break
			}
		}
		if hit == nil {
			t.Errorf("%s (%s) not discovered; got %d results", game, launcher, len(found))
			continue
		}
		if !strings.Contains(hit.Name, launcher) {
			t.Errorf("%s labelled %q, want it to mention %s", game, hit.Name, launcher)
		}
	}
}

// TestScanWinePrefixes_SkipsNoise keeps the new pass from filling the grid
// with Wine plumbing and shader caches.
func TestScanWinePrefixes_SkipsNoise(t *testing.T) {
	home := t.TempDir()
	prefix := filepath.Join(home, ".local", "share", "wineprefixes", "test")

	mkPrefixSave(t, prefix, "deck", filepath.Join("AppData", "Roaming"), "RealGame")
	// Vendor plumbing, a cache, and a hex-hash cache dir — none are saves.
	mkPrefixSave(t, prefix, "deck", filepath.Join("AppData", "Roaming"), "Microsoft")
	mkPrefixSave(t, prefix, "deck", filepath.Join("AppData", "Roaming"), "ShaderCache")
	mkPrefixSave(t, prefix, "deck", filepath.Join("AppData", "Roaming"),
		"00767f4da4e990265f6f7ce9e2273256043161ab200bb1c35d2f2393a05e4c2f")

	found := (&Scanner{GOOS: "linux", HomeDir: home}).scanWinePrefixes(map[string]bool{})

	for _, d := range found {
		for _, bad := range []string{"Microsoft", "ShaderCache", "00767f4d"} {
			if strings.Contains(d.SavePath, bad) {
				t.Errorf("offered noise: %s", d.SavePath)
			}
		}
	}
	if len(found) != 1 {
		t.Errorf("expected only RealGame, got %d: %+v", len(found), found)
	}
}

// TestScanWinePrefixes_RespectsSeen pins that a precise hit from an earlier
// pass isn't shadowed by this broader one.
func TestScanWinePrefixes_RespectsSeen(t *testing.T) {
	home := t.TempDir()
	prefix := filepath.Join(home, ".wine")
	save := mkPrefixSave(t, prefix, "deck", filepath.Join("AppData", "Roaming"), "AlreadyFound")

	abs, err := filepath.Abs(save)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{abs: true}

	if found := (&Scanner{GOOS: "linux", HomeDir: home}).scanWinePrefixes(seen); len(found) != 0 {
		t.Errorf("re-offered an already-discovered path: %+v", found)
	}
}

// TestScanWinePrefixes_NonLinuxNoop keeps Windows scans from paying for this.
func TestScanWinePrefixes_NonLinuxNoop(t *testing.T) {
	home := t.TempDir()
	mkPrefixSave(t, filepath.Join(home, ".wine"), "u", filepath.Join("AppData", "Roaming"), "G")

	if found := (&Scanner{GOOS: "windows", HomeDir: home}).scanWinePrefixes(map[string]bool{}); len(found) != 0 {
		t.Errorf("wine prefix scan should be Linux-only, got %+v", found)
	}
}
