package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A preset with a wrong path costs nothing at runtime — dirExists simply never
// matches — which is exactly the problem. It looks like support, ships as
// support, and detects nothing, and the user reporting it has no way to tell
// that apart from a bug. So each new entry is exercised against a directory
// built at the path it claims.
func TestRetroEmulatorPresetsResolveOnLinux(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	// The first Linux candidate for each new preset, which is the one a
	// native install actually uses.
	cases := map[string]string{
		"mupen64plus": ".local/share/mupen64plus/save",
		"simple64":    ".local/share/simple64/saves",
		"melonds":     ".config/melonDS",
		"desmume":     ".config/desmume",
		"mgba":        ".config/mgba",
		"sameboy":     ".local/share/sameboy",
		"snes9x":      ".config/snes9x",
		"bsnes":       ".local/share/bsnes",
		"fceux":       ".fceux/sav",
		"mesen":       ".local/share/Mesen2/Saves",
		"blastem":     ".local/share/blastem",
	}

	for id, rel := range cases {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, filepath.FromSlash(rel))
			if err := os.MkdirAll(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "game.sav"), []byte("x"), 0o666); err != nil {
				t.Fatal(err)
			}

			sc := &Scanner{CacheFile: filepath.Join(t.TempDir(), "cache.json"), GOOS: "linux", HomeDir: home}
			for _, d := range sc.Scan(nil) {
				if d.ID == id {
					return
				}
			}
			t.Errorf("%s did not detect a save dir at ~/%s — the preset path does not match what it claims", id, rel)
		})
	}
}

// Every preset must carry a Linux path unless the emulator genuinely is
// Windows-only. A Windows-only entry is a real choice (Cemu, Xenia, Project64);
// an accidentally Windows-only one silently excludes every Deck user, which is
// the audience most likely to be running these.
func TestRetroEmulatorPresetsCoverLinuxExceptWindowsOnlyOnes(t *testing.T) {
	// Emulators with no Linux build worth detecting.
	windowsOnly := map[string]bool{
		"xenia": true, "cemu": true, "project64": true,
	}

	for _, p := range presetDefs {
		if p.Type != "emulator" || windowsOnly[p.ID] {
			continue
		}
		if len(p.LinuxPath) == 0 {
			t.Errorf("%s (%s) has no Linux path and is not marked Windows-only", p.ID, p.Name)
		}
	}
}

// Preset ids end up in game ids and in the save-path lookup, so a duplicate
// would have two emulators quietly fighting over one entry.
func TestPresetIDsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, p := range presetDefs {
		if prev, dup := seen[p.ID]; dup {
			t.Errorf("duplicate preset id %q used by both %q and %q", p.ID, prev, p.Name)
		}
		seen[p.ID] = p.Name
	}
}

// Windows paths use %VAR% placeholders that get expanded at scan time. A typo
// in the variable name resolves to something meaningless rather than failing,
// so the set of names in use is worth pinning.
func TestPresetWindowsPathsUseKnownVariables(t *testing.T) {
	known := map[string]bool{
		"%APPDATA%": true, "%LOCALAPPDATA%": true, "%USERPROFILE%": true,
		"%PUBLIC%": true, "%PROGRAMDATA%": true,
	}
	for _, p := range presetDefs {
		if p.Path == "" || !strings.HasPrefix(p.Path, "%") {
			continue
		}
		v := p.Path[:strings.Index(p.Path[1:], "%")+2]
		if !known[v] {
			t.Errorf("%s uses unknown path variable %q in %q", p.ID, v, p.Path)
		}
	}
}
