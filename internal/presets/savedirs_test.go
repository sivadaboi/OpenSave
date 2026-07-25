package presets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func mkFile(t *testing.T, parts ...string) {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
}

func rel(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

// TestFindSaveDirsUnder_UbisoftLayout is the reported case: Assassin's Creed
// keeping saves inside the game folder under savegames/<numeric id>, which no
// preset can enumerate and which the Unreal-only install scan missed.
func TestFindSaveDirsUnder_UbisoftLayout(t *testing.T) {
	install := t.TempDir()
	mkFile(t, install, "savegames", "65043", "1.save")
	mkFile(t, install, "AC4BFSP.exe")
	mkFile(t, install, "Data_Win", "asset.forge")

	got := rel(t, install, findSaveDirsUnder(install))
	want := []string{"savegames"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("findSaveDirsUnder = %v, want %v", got, want)
	}
}

// TestFindSaveDirsUnder_PrefersSpecificChild pins that a generic container
// ("Saved") resolves to its more specific child ("SaveGames") rather than
// offering the folder full of logs and config.
func TestFindSaveDirsUnder_PrefersSpecificChild(t *testing.T) {
	install := t.TempDir()
	mkFile(t, install, "MyGame", "Saved", "SaveGames", "slot0.sav")
	mkFile(t, install, "MyGame", "Saved", "Logs", "game.log")
	mkFile(t, install, "MyGame", "Saved", "Config", "engine.ini")

	got := rel(t, install, findSaveDirsUnder(install))
	if len(got) != 1 || got[0] != "MyGame/Saved/SaveGames" {
		t.Errorf("findSaveDirsUnder = %v, want [MyGame/Saved/SaveGames]", got)
	}
}

// TestFindSaveDirsUnder_SkipsCacheAndEmpty covers the noise controls: shader
// and DX12 caches are never offered, and an empty save folder isn't either.
func TestFindSaveDirsUnder_SkipsCacheAndEmpty(t *testing.T) {
	install := t.TempDir()
	mkFile(t, install, "DX12Cache", "pipeline.bin")
	mkFile(t, install, "ShaderCache", "s.bin")
	mkFile(t, install, "AnvilDX12Cache", "s.bin")
	mkFile(t, install, "Shader Cache", "s.bin")
	if err := os.MkdirAll(filepath.Join(install, "Saves"), 0o777); err != nil {
		t.Fatal(err)
	} // empty save dir

	if got := findSaveDirsUnder(install); len(got) != 0 {
		t.Errorf("expected nothing (caches skipped, empty save dir ignored), got %v", rel(t, install, got))
	}

	// Once it has content, the save folder is offered.
	mkFile(t, install, "Saves", "slot1.dat")
	got := rel(t, install, findSaveDirsUnder(install))
	if len(got) != 1 || got[0] != "Saves" {
		t.Errorf("findSaveDirsUnder = %v, want [Saves]", got)
	}
}

// TestFindSaveDirsUnder_DoesNotDescendPastMatch ensures a save folder's own
// subfolders don't each become separate results.
func TestFindSaveDirsUnder_DoesNotDescendPastMatch(t *testing.T) {
	install := t.TempDir()
	mkFile(t, install, "savegames", "profile1", "a.sav")
	mkFile(t, install, "savegames", "profile2", "b.sav")

	got := rel(t, install, findSaveDirsUnder(install))
	if len(got) != 1 || got[0] != "savegames" {
		t.Errorf("findSaveDirsUnder = %v, want just [savegames]", got)
	}
}

// TestFindSaveDirsUnder_DepthAndFanoutBounds keeps the heuristic from turning
// a scan into a full-disk walk.
func TestFindSaveDirsUnder_DepthAndFanoutBounds(t *testing.T) {
	install := t.TempDir()
	// Deeper than saveScanMaxDepth — must not be found.
	mkFile(t, install, "a", "b", "c", "d", "Saves", "deep.sav")
	if got := findSaveDirsUnder(install); len(got) != 0 {
		t.Errorf("save dir below the depth limit should be ignored, got %v", rel(t, install, got))
	}

	// A directory with an implausible number of subfolders is skipped.
	wide := t.TempDir()
	for i := 0; i < saveScanMaxFanout+5; i++ {
		if err := os.MkdirAll(filepath.Join(wide, "asset"+strings.Repeat("0", 1)+string(rune('a'+i%26))+itoa(i)), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(t, wide, "Saves", "x.sav")
	if got := findSaveDirsUnder(wide); len(got) != 0 {
		t.Errorf("wide asset dir should be skipped, got %v", rel(t, wide, got))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestIsCacheDirName(t *testing.T) {
	cache := []string{
		"DX12Cache", "dx12cache", "DXCache", "D3DSCache", "ShaderCache",
		"Shader Cache", "shader_cache", "AnvilDX12Cache", "GPUCache",
		"PipelineCache", "cache", "Logs", "CrashDumps", "temp",
		"DerivedDataCache",
	}
	for _, n := range cache {
		if !isCacheDirName(n) {
			t.Errorf("isCacheDirName(%q) = false, want true", n)
		}
	}
	notCache := []string{
		"savegames", "Saves", "SaveData", "65043", "Profiles", "Data_Win",
		"MyGame", "SaveGames",
	}
	for _, n := range notCache {
		if isCacheDirName(n) {
			t.Errorf("isCacheDirName(%q) = true, want false", n)
		}
	}
}

func TestLooksLikeSaveDirName(t *testing.T) {
	yes := []string{"Saves", "savegames", "SaveData", "AutoSave", "SAVE", "saved", "save_data"}
	for _, n := range yes {
		if !looksLikeSaveDirName(n) {
			t.Errorf("looksLikeSaveDirName(%q) = false, want true", n)
		}
	}
	no := []string{"65043", "Binaries", "Config", "Logs", "Data"}
	for _, n := range no {
		if looksLikeSaveDirName(n) {
			t.Errorf("looksLikeSaveDirName(%q) = true, want false", n)
		}
	}
}
