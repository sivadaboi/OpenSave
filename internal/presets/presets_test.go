package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath_SubstitutesEnvVars(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)

	got := ResolvePath("%APPDATA%/Ryujinx/bis/user/save")
	want := filepath.Join(appdata, "Ryujinx", "bis", "user", "save")
	if got != want {
		t.Errorf("ResolvePath = %q, want %q", got, want)
	}
}

func TestResolvePath_CaseInsensitiveVars(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)

	got := ResolvePath("%appdata%/RetroArch/saves")
	want := filepath.Join(appdata, "RetroArch", "saves")
	if got != want {
		t.Errorf("ResolvePath (lowercase var) = %q, want %q", got, want)
	}
}

// isolateEnv points every scan location at empty temp dirs so tests never
// touch the real machine's Steam/emulator installs.
func isolateEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APPDATA", filepath.Join(t.TempDir(), "appdata"))
	t.Setenv("PUBLIC", filepath.Join(t.TempDir(), "public"))
	t.Setenv("PROGRAMDATA", filepath.Join(t.TempDir(), "programdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "localappdata"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
}

// hermeticScanner returns a Scanner that cannot reach the machine's real
// Steam userdata or the network.
func hermeticScanner(steamPaths ...string) *Scanner {
	if steamPaths == nil {
		steamPaths = []string{}
	}
	// These tests set up Windows-style save layouts (%APPDATA% etc.), so
	// pin the scan to Windows conventions regardless of the host OS the
	// tests run on (CI runs them on Linux too).
	return &Scanner{SteamUserdataPaths: steamPaths, GOOS: "windows"}
}

func TestScan_DetectsEmulatorPreset(t *testing.T) {
	isolateEnv(t)
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)

	ryujinxSaves := filepath.Join(appdata, "Ryujinx", "bis", "user", "save")
	if err := os.MkdirAll(ryujinxSaves, 0o777); err != nil {
		t.Fatal(err)
	}

	sc := hermeticScanner()
	found := sc.Scan(nil)

	var hit *DiscoveredSave
	for i := range found {
		if found[i].ID == "ryujinx" {
			hit = &found[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("Ryujinx preset not discovered; got %+v", found)
	}
	if hit.Type != "emulator" || hit.SavePath != ryujinxSaves {
		t.Errorf("Ryujinx discovery wrong: %+v", hit)
	}
}

func TestScan_WrapperSubfoldersBecomeGames(t *testing.T) {
	isolateEnv(t)
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)

	goldberg := filepath.Join(appdata, "Goldberg SteamEmu Saves")
	for _, sub := range []string{"1245620", "settings", "MyIndieGame"} {
		if err := os.MkdirAll(filepath.Join(goldberg, sub), 0o777); err != nil {
			t.Fatal(err)
		}
	}

	sc := hermeticScanner()
	found := sc.Scan(nil)

	byID := map[string]DiscoveredSave{}
	for _, d := range found {
		byID[d.ID] = d
	}

	appIDEntry, ok := byID["goldberg-1245620"]
	if !ok {
		t.Fatalf("goldberg AppID subfolder not discovered; got %v", found)
	}
	if appIDEntry.AppID != "1245620" {
		t.Errorf("numeric wrapper subfolder should carry its AppID, got %+v", appIDEntry)
	}
	// AppID 1245620 is in the offline dictionary -> real title, no network.
	if appIDEntry.Name != "Elden Ring" {
		t.Errorf("offline dictionary should resolve 1245620 to Elden Ring, got %q", appIDEntry.Name)
	}

	if _, ok := byID["goldberg-settings"]; ok {
		t.Error("wrapper system dir 'settings' must be excluded")
	}
	if _, ok := byID["goldberg-MyIndieGame"]; !ok {
		t.Error("non-numeric wrapper subfolder should still be discovered")
	}
}

func TestScan_SteamUserdata(t *testing.T) {
	isolateEnv(t)

	userdata := filepath.Join(t.TempDir(), "userdata")
	if err := os.MkdirAll(filepath.Join(userdata, "123456789", "413150"), 0o777); err != nil {
		t.Fatal(err)
	}

	sc := hermeticScanner(userdata)
	found := sc.Scan(nil)

	var hit *DiscoveredSave
	for i := range found {
		if found[i].ID == "steam-123456789-413150" {
			hit = &found[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("steam userdata game not discovered; got %+v", found)
	}
	if hit.AppID != "413150" {
		t.Errorf("AppID = %q, want 413150", hit.AppID)
	}
	if hit.Name != "Stardew Valley" {
		t.Errorf("offline dictionary should resolve name, got %q", hit.Name)
	}
}

func TestScan_CustomPathsAndNameInference(t *testing.T) {
	isolateEnv(t)

	custom := filepath.Join(t.TempDir(), "GameSaves")
	if err := os.MkdirAll(filepath.Join(custom, "Elden Ring"), 0o777); err != nil {
		t.Fatal(err)
	}

	sc := hermeticScanner()
	found := sc.Scan([]string{custom})

	var hit *DiscoveredSave
	for i := range found {
		if found[i].SavePath == filepath.Join(custom, "Elden Ring") {
			hit = &found[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("custom path subfolder not discovered; got %+v", found)
	}
	// Name "Elden Ring" should infer AppID 1245620 via the name index.
	if hit.AppID != "1245620" {
		t.Errorf("name-based AppID inference failed: %+v", hit)
	}
}

func TestResolveNames_UsesResolverAndCachesResult(t *testing.T) {
	isolateEnv(t)

	userdata := filepath.Join(t.TempDir(), "userdata")
	// Unknown AppID (not in offline dictionary).
	if err := os.MkdirAll(filepath.Join(userdata, "1", "99999999"), 0o777); err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(t.TempDir(), "steam-app-cache.json")
	calls := 0
	sc := &Scanner{
		CacheFile:          cacheFile,
		SteamUserdataPaths: []string{userdata},
		ResolveAppName: func(appID string) string {
			calls++
			if appID == "99999999" {
				return "Obscure Test Game"
			}
			return ""
		},
	}

	found := sc.Scan(nil)
	var hit *DiscoveredSave
	for i := range found {
		if found[i].AppID == "99999999" {
			hit = &found[i]
		}
	}
	if hit == nil || hit.Name != "Obscure Test Game" {
		t.Fatalf("resolver name not applied: %+v", hit)
	}
	if calls != 1 {
		t.Errorf("resolver called %d times, want 1", calls)
	}

	// Second scan must hit the disk cache, not the resolver.
	found = sc.Scan(nil)
	for i := range found {
		if found[i].AppID == "99999999" && found[i].Name != "Obscure Test Game" {
			t.Errorf("cached name not used on second scan: %+v", found[i])
		}
	}
	if calls != 1 {
		t.Errorf("resolver should not be called again after caching, total calls = %d", calls)
	}
}

// TestScanLocalLowUnitySaves: the Unity convention LocalLow/<Company>/<Game>
// (PICO PARK, Hollow Knight, …) must be discovered, with vendor noise and
// hex-named cache dirs filtered out.
func TestScanLocalLowUnitySaves(t *testing.T) {
	low := t.TempDir()
	mustMkFile(t, filepath.Join(low, "TECOPARK", "PICO PARK 2", "Steam", "765611", "save.dat"))
	mustMkFile(t, filepath.Join(low, "Team Cherry", "Hollow Knight", "user1.dat"))
	mustMkFile(t, filepath.Join(low, "Microsoft", "CLR", "junk.log"))
	mustMkFile(t, filepath.Join(low, "00767f4da4e990265f6f7ce9e2273256043161ab200bb1c35d2f2393a05e4c2f", "x", "y"))
	if err := os.MkdirAll(filepath.Join(low, "EmptyCo", "EmptyGame"), 0o777); err != nil {
		t.Fatal(err)
	}

	sc := &Scanner{LocalLowDir: low, SteamRoots: []string{}, SteamUserdataPaths: []string{}}
	found := sc.Scan(nil)

	names := map[string]string{}
	for _, d := range found {
		if strings.HasPrefix(d.ID, "unity-") {
			names[d.Name] = d.SavePath
		}
	}
	if _, ok := names["PICO PARK 2"]; !ok {
		t.Errorf("PICO PARK 2 not discovered; got %v", names)
	}
	if _, ok := names["Hollow Knight"]; !ok {
		t.Errorf("Hollow Knight not discovered; got %v", names)
	}
	if len(names) != 2 {
		t.Errorf("vendor/hash/empty dirs must be filtered, got %v", names)
	}

	// Known Unity titles get an AppID inferred for cover art.
	for _, d := range found {
		if d.Name == "Hollow Knight" && d.AppID != "367520" {
			t.Errorf("Hollow Knight AppID = %q, want 367520", d.AppID)
		}
	}
}

// TestScanSteamLibrariesAndInstallDirSaves: libraryfolders.vdf must be
// followed to secondary drives; installed games are named from their
// appmanifests; and game folders next to steamapps (cracked/portable UE
// installs) are probed for <Project>/Saved/SaveGames.
func TestScanSteamLibrariesAndInstallDirSaves(t *testing.T) {
	root := t.TempDir()
	lib2 := t.TempDir()

	// Root steam install whose libraryfolders.vdf points at lib2.
	// Steam writes Windows paths with doubled backslashes in the vdf.
	vdf := `"libraryfolders" { "0" { "path" "` + strings.ReplaceAll(root, `\`, `\\`) + `" } "1" { "path" "` + strings.ReplaceAll(lib2, `\`, `\\`) + `" } }`
	mustWriteFile(t, filepath.Join(root, "steamapps", "libraryfolders.vdf"), vdf)

	// An installed Steam game on lib2 whose saves use the UE in-install
	// convention.
	acf := `"AppState" { "appid" "2358720" "name" "Black Myth: Wukong" "installdir" "BlackMythWukong" }`
	mustWriteFile(t, filepath.Join(lib2, "steamapps", "appmanifest_2358720.acf"), acf)
	mustMkFile(t, filepath.Join(lib2, "steamapps", "common", "BlackMythWukong", "b1", "Saved", "SaveGames", "slot1.sav"))

	// A cracked install sitting NEXT to steamapps on the library drive.
	mustMkFile(t, filepath.Join(lib2, "Some Cracked Game", "proj", "Saved", "SaveGames", "save.sav"))

	// Steam userdata under the root: a real game named via manifest… and
	// client plumbing dirs that must be skipped.
	mustMkFile(t, filepath.Join(root, "userdata", "123", "2358720", "remote", "cloud.sav"))
	mustMkFile(t, filepath.Join(root, "userdata", "123", "7", "junk"))
	mustMkFile(t, filepath.Join(root, "userdata", "123", "760", "screenshots.vdf"))

	sc := &Scanner{LocalLowDir: t.TempDir(), SteamRoots: []string{root}}
	found := sc.Scan(nil)

	byID := map[string]DiscoveredSave{}
	for _, d := range found {
		byID[d.ID] = d
	}

	if d, ok := byID["steamlib-2358720"]; !ok {
		t.Fatalf("installed Steam game save not discovered; got %v", byID)
	} else {
		if d.Name != "Black Myth: Wukong" || d.AppID != "2358720" {
			t.Errorf("steamlib entry = %+v, want manifest name+appid", d)
		}
		if filepath.Base(filepath.Dir(filepath.Dir(d.SavePath))) != "b1" {
			t.Errorf("steamlib save path = %q, want the b1/Saved/SaveGames dir", d.SavePath)
		}
	}

	if d, ok := byID["installdir-some-cracked-game"]; !ok {
		t.Errorf("cracked install next to steamapps not discovered; got %v", byID)
	} else if d.Name != "Some Cracked Game" {
		t.Errorf("installdir entry name = %q", d.Name)
	}

	if d, ok := byID["steam-123-2358720"]; !ok {
		t.Errorf("userdata save not discovered")
	} else if d.Name != "Black Myth: Wukong" {
		t.Errorf("userdata entry should take the manifest name, got %q", d.Name)
	}
	if _, ok := byID["steam-123-7"]; ok {
		t.Error("userdata id 7 (client config) must be skipped")
	}
	if _, ok := byID["steam-123-760"]; ok {
		t.Error("userdata id 760 (screenshots) must be skipped")
	}
}

func mustMkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}
