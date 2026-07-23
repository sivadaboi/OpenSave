package presets

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func names(saves []DiscoveredSave) []string {
	out := make([]string, len(saves))
	for i, s := range saves {
		out[i] = s.Name
	}
	return out
}

func TestFilterExcluded(t *testing.T) {
	// Build OS-native paths so the test is portable across separators.
	gse := filepath.Join("C:", "Users", "me", "GSE saves")
	gseGame := filepath.Join(gse, "Skyrim")
	gseOther := filepath.Join("C:", "Users", "me", "GSE savesOther") // boundary
	steamGame := filepath.Join("C:", "Program Files", "Steam", "Skyrim")

	saves := []DiscoveredSave{
		{ID: "1", Name: "gse-root", SavePath: gse},
		{ID: "2", Name: "gse-game", SavePath: gseGame},
		{ID: "3", Name: "gse-other", SavePath: gseOther},
		{ID: "4", Name: "steam-game", SavePath: steamGame},
	}

	got := FilterExcluded(saves, []string{gse})
	gotNames := names(got)

	// The excluded dir itself and anything under it are gone.
	for _, banned := range []string{"gse-root", "gse-game"} {
		if contains(gotNames, banned) {
			t.Errorf("expected %q to be excluded, got %v", banned, gotNames)
		}
	}
	// A sibling that merely shares the prefix must survive (boundary-aware).
	if !contains(gotNames, "gse-other") {
		t.Errorf("boundary match wrongly excluded gse-other: %v", gotNames)
	}
	// Unrelated saves are untouched.
	if !contains(gotNames, "steam-game") {
		t.Errorf("steam-game should not be excluded: %v", gotNames)
	}
}

func TestFilterExcludedEmptyInputs(t *testing.T) {
	saves := []DiscoveredSave{{ID: "1", Name: "a", SavePath: filepath.Join("C:", "x")}}

	if got := FilterExcluded(saves, nil); len(got) != 1 {
		t.Errorf("nil excludes should pass everything through, got %d", len(got))
	}
	if got := FilterExcluded(saves, []string{"", "   "}); len(got) != 1 {
		t.Errorf("blank excludes should pass everything through, got %d", len(got))
	}
	if got := FilterExcluded(nil, []string{"C:\\x"}); got != nil {
		t.Errorf("nil saves should return nil, got %v", got)
	}
}

func TestFilterExcludedTrailingSeparatorAndDot(t *testing.T) {
	dir := filepath.Join("D:", "Games", "saves")
	under := filepath.Join(dir, "GameA")
	saves := []DiscoveredSave{{ID: "1", Name: "under", SavePath: under}}

	// A messy exclude (trailing separator + "/.") must still match after Clean.
	messy := dir + string(filepath.Separator) + "."
	if got := FilterExcluded(saves, []string{messy}); len(got) != 0 {
		t.Errorf("cleaned exclude should match; got %v", names(got))
	}
}

func TestFilterExcludedCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("case-insensitive matching only applies to Windows/macOS")
	}
	dir := filepath.Join("C:", "Users", "Me", "GSE Saves")
	under := filepath.Join("C:", "users", "me", "gse saves", "GameA")
	saves := []DiscoveredSave{{ID: "1", Name: "under", SavePath: under}}

	if got := FilterExcluded(saves, []string{dir}); len(got) != 0 {
		t.Errorf("case-insensitive exclude should match; got %v", names(got))
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
