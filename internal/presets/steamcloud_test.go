package presets

import (
	"os"
	"path/filepath"
	"testing"
)

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

// Steam writes the saves under remote/ and keeps remotecache.vdf beside it as
// its own sync index. Tracking the parent picks up both, which users report as
// "it doesn't detect the exact save game location" — and worse, remotecache
// differs per machine and is rewritten on every sync, so two devices diverge
// constantly with no save having changed.
func TestScanSteamUserdata_TracksRemoteNotTheAppIDFolder(t *testing.T) {
	root := t.TempDir()
	userdata := filepath.Join(root, "userdata")
	appDir := filepath.Join(userdata, "76561198000000000", "413150")

	mkfile(t, filepath.Join(appDir, "remote", "Slot1.dat"), "farm save")
	mkfile(t, filepath.Join(appDir, "remotecache.vdf"), `"413150" { "Slot1.dat" { "size" "1024" } }`)

	sc := &Scanner{SteamUserdataPaths: []string{userdata}}
	found := sc.scanSteamUserdata(map[string]bool{}, map[string]string{})

	if len(found) != 1 {
		t.Fatalf("expected one discovered save, got %d: %+v", len(found), found)
	}
	want := filepath.Join(appDir, "remote")
	if found[0].SavePath != want {
		t.Errorf("SavePath = %q, want %q — the AppID folder also holds remotecache.vdf,"+
			" which is Steam's per-machine sync index and not a save",
			found[0].SavePath, want)
	}
	if found[0].AppID != "413150" {
		t.Errorf("AppID = %q, want 413150 — narrowing to remote/ must not lose the id", found[0].AppID)
	}
}

// Games that write straight into the AppID folder must keep working.
func TestScanSteamUserdata_FallsBackWhenNoRemoteFolder(t *testing.T) {
	root := t.TempDir()
	userdata := filepath.Join(root, "userdata")
	appDir := filepath.Join(userdata, "76561198000000000", "220")
	mkfile(t, filepath.Join(appDir, "save.sav"), "half-life 2")

	sc := &Scanner{SteamUserdataPaths: []string{userdata}}
	found := sc.scanSteamUserdata(map[string]bool{}, map[string]string{})

	if len(found) != 1 {
		t.Fatalf("expected one discovered save, got %d", len(found))
	}
	if found[0].SavePath != appDir {
		t.Errorf("SavePath = %q, want the AppID folder %q", found[0].SavePath, appDir)
	}
}

// resolveSteamCloudDir is what the repack-wrapper branch uses too: every Steam
// emulator copies Steam's layout, putting achievements, stats and playtime
// counters next to remote/. Those change every session and would otherwise be
// synced as if they were saves.
func TestResolveSteamCloudDir(t *testing.T) {
	t.Run("prefers remote when present", func(t *testing.T) {
		appDir := t.TempDir()
		mkfile(t, filepath.Join(appDir, "remote", "save.dat"), "progress")
		mkfile(t, filepath.Join(appDir, "achievements.json"), "{}")
		mkfile(t, filepath.Join(appDir, "time.txt"), "3600")

		if got, want := resolveSteamCloudDir(appDir), filepath.Join(appDir, "remote"); got != want {
			t.Errorf("resolveSteamCloudDir = %q, want %q", got, want)
		}
	})

	t.Run("falls back without remote", func(t *testing.T) {
		appDir := t.TempDir()
		mkfile(t, filepath.Join(appDir, "save.dat"), "progress")
		if got := resolveSteamCloudDir(appDir); got != appDir {
			t.Errorf("resolveSteamCloudDir = %q, want %q", got, appDir)
		}
	})

	t.Run("empty remote still wins", func(t *testing.T) {
		// A game installed but not yet played has an empty remote/. That is
		// still where its saves will appear; the parent is not.
		appDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(appDir, "remote"), 0o777); err != nil {
			t.Fatal(err)
		}
		mkfile(t, filepath.Join(appDir, "remotecache.vdf"), "")
		if got, want := resolveSteamCloudDir(appDir), filepath.Join(appDir, "remote"); got != want {
			t.Errorf("resolveSteamCloudDir = %q, want %q", got, want)
		}
	})
}

// A repacked game whose wrapper folder holds the game's own Unreal-style save
// tree plus a playtime counter. Reported from the field: the scanner offered
// the folder containing playtime.txt, so the first sync carried the counter
// and missed the point.
//
// "BBQ" is the shape of the real report — repacks often rename the container
// to the game's internal project name rather than its title.
func TestResolveGameContainerDir_NarrowsToNestedSaveTree(t *testing.T) {
	container := filepath.Join(t.TempDir(), "BBQ")
	mkfile(t, filepath.Join(container, "playtime.txt"), "7200")
	mkfile(t, filepath.Join(container, "Saved", "SaveGames", "Slot0.sav"), "khazan progress")

	want := filepath.Join(container, "Saved", "SaveGames")
	if got := resolveGameContainerDir(container); got != want {
		t.Errorf("resolveGameContainerDir = %q, want %q — offering the container tracks "+
			"playtime.txt, which changes every session on each device independently", got, want)
	}
}

// remote/ is the stronger signal and must win over a name match.
func TestResolveGameContainerDir_PrefersRemoteOverNameMatch(t *testing.T) {
	container := filepath.Join(t.TempDir(), "480")
	mkfile(t, filepath.Join(container, "remote", "save.dat"), "real save")
	mkfile(t, filepath.Join(container, "SaveBackups", "old.dat"), "a backup, not the live save")

	want := filepath.Join(container, "remote")
	if got := resolveGameContainerDir(container); got != want {
		t.Errorf("resolveGameContainerDir = %q, want %q", got, want)
	}
}

// Ambiguity is left alone: guessing between candidates risks syncing the wrong
// one, and the user can see and correct a container.
func TestResolveGameContainerDir_LeavesAmbiguousLayoutsAlone(t *testing.T) {
	container := filepath.Join(t.TempDir(), "game")
	mkfile(t, filepath.Join(container, "Saves", "a.sav"), "one")
	mkfile(t, filepath.Join(container, "SaveData", "b.sav"), "two")

	if got := resolveGameContainerDir(container); got != container {
		t.Errorf("resolveGameContainerDir = %q, want the container %q when several "+
			"save folders are plausible", got, container)
	}
}

// No save-shaped folder anywhere: the container is the answer.
func TestResolveGameContainerDir_KeepsFlatLayouts(t *testing.T) {
	container := filepath.Join(t.TempDir(), "flat")
	mkfile(t, filepath.Join(container, "profile.sav"), "data")
	if got := resolveGameContainerDir(container); got != container {
		t.Errorf("resolveGameContainerDir = %q, want %q", got, container)
	}
}
