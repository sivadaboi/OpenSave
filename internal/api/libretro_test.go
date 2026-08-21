package api

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLibretroRomName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// The region tag is part of the No-Intro name and must survive.
		{"Super Mario World (USA).srm", "Super Mario World (USA)"},
		{"Chrono Trigger (USA).sav", "Chrono Trigger (USA)"},
		// Forbidden characters become "_", matching how the collection
		// names its own files.
		{"Pokemon: Red/Blue (USA).srm", "Pokemon_ Red_Blue (USA)"},
		{"Sonic & Knuckles (World).srm", "Sonic _ Knuckles (World)"},
		// RetroArch's numbered save states leave ".state" behind after one
		// extension strip; both forms must reduce to the ROM name.
		{"Final Fantasy VI (USA).state", "Final Fantasy VI (USA)"},
		{"Final Fantasy VI (USA).state1", "Final Fantasy VI (USA)"},
		// A name with no extension is already the ROM name.
		{"Tetris (World)", "Tetris (World)"},
	}
	for _, tc := range tests {
		if got := libretroRomName(tc.in); got != tc.want {
			t.Errorf("libretroRomName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLibretroCandidatesFromCoreFolder(t *testing.T) {
	// "Sort saves by core name" puts the core directly above the file, and
	// that names the system outright — one candidate, no guessing.
	rel := filepath.Join("Snes9x", "Super Mario World (USA).srm")
	got := libretroCandidates(rel)
	want := "Nintendo - Super Nintendo Entertainment System"
	if len(got) != 1 || got[0] != want {
		t.Errorf("libretroCandidates(%q) = %v, want exactly [%q]", rel, got, want)
	}
}

func TestLibretroCandidatesFromExtension(t *testing.T) {
	// RetroArch's default layout is a flat saves/ folder, so the extension
	// is the only signal and .srm legitimately spans several systems.
	got := libretroCandidates("Super Mario World (USA).srm")
	if len(got) < 2 {
		t.Fatalf("expected several .srm candidates, got %v", got)
	}
	if got[0] != "Nintendo - Super Nintendo Entertainment System" {
		t.Errorf("most common .srm system should be tried first, got %q", got[0])
	}
	// An extension we have no mapping for must yield nothing rather than a
	// wrong guess: a wrong cover is worse than no cover.
	if got := libretroCandidates("memcard.bin"); len(got) != 0 {
		t.Errorf("unknown extension should have no candidates, got %v", got)
	}
}

func TestLibretroCandidatesIgnoresNonCoreFolder(t *testing.T) {
	// A user's own foldering is not a core name; fall back to the extension
	// rather than treating "Backups" as a system.
	got := libretroCandidates(filepath.Join("Backups", "Chrono Trigger (USA).srm"))
	if len(got) == 0 {
		t.Fatal("expected extension fallback for a non-core folder")
	}
	if got[0] != "Nintendo - Super Nintendo Entertainment System" {
		t.Errorf("expected .srm fallback list, got %v", got)
	}
}

func TestLibretroThumbURL(t *testing.T) {
	got := libretroThumbURL("Nintendo - Game Boy Advance", "Pokemon - Emerald Version (USA, Europe)")
	if !strings.HasPrefix(got, libretroThumbBase+"/Nintendo%20-%20Game%20Boy%20Advance/Named_Boxarts/") {
		t.Errorf("system segment not escaped as expected: %q", got)
	}
	if !strings.HasSuffix(got, ".png") {
		t.Errorf("URL should end in .png, got %q", got)
	}
	// Spaces, parens and commas in the ROM name must be escaped, not raw.
	if strings.Contains(got, " ") {
		t.Errorf("unescaped space in URL: %q", got)
	}
}

func TestLibretroThumbURLRejectsUnknownSystem(t *testing.T) {
	// The system segment is what keeps arbitrary strings out of the URL
	// path — the same property isNumericID gives the Steam CDN template.
	for _, sys := range []string{"", "../../etc", "Nintendo - Made Up Console"} {
		if got := libretroThumbURL(sys, "Anything"); got != "" {
			t.Errorf("libretroThumbURL(%q, ...) = %q, want \"\"", sys, got)
		}
	}
	if got := libretroThumbURL("Nintendo - Game Boy Advance", ""); got != "" {
		t.Errorf("empty ROM name should yield no URL, got %q", got)
	}
}

func TestLibretroSystemSetCoversAllMaps(t *testing.T) {
	// Every system either map can produce must be constructible, or that
	// entry is dead code that silently never resolves a cover.
	for core, sys := range libretroCoreSystems {
		if libretroThumbURL(sys, "X") == "" {
			t.Errorf("core %q maps to %q, which libretroThumbURL rejects", core, sys)
		}
	}
	for ext, cands := range libretroExtSystems {
		for _, sys := range cands {
			if libretroThumbURL(sys, "X") == "" {
				t.Errorf("ext %q lists %q, which libretroThumbURL rejects", ext, sys)
			}
		}
	}
}
