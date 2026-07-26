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
