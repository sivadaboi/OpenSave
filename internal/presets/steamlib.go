package presets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Steam library discovery: find the real Steam install (registry on
// Windows), follow libraryfolders.vdf to every library drive, and parse
// appmanifest_*.acf files for installed games — exact names and AppIDs,
// no network needed. This is what catches games installed to a second
// drive (e.g. D:\Games) that the hardcoded C:\ paths never saw.

// steamApp is one installed Steam game parsed from an appmanifest.
type steamApp struct {
	AppID      string
	Name       string
	InstallDir string // absolute path under <library>\steamapps\common
}

var (
	vdfPathRe       = regexp.MustCompile(`"path"\s+"((?:[^"\\]|\\.)*)"`)
	acfAppIDRe      = regexp.MustCompile(`"appid"\s+"([^"]*)"`)
	acfNameRe       = regexp.MustCompile(`"name"\s+"([^"]*)"`)
	acfInstallDirRe = regexp.MustCompile(`"installdir"\s+"([^"]*)"`)
)

// steamRootDirs returns candidate Steam install roots: the registry
// location first (authoritative on Windows), then the well-known defaults.
func (sc *Scanner) steamRootDirs() []string {
	if sc.SteamRoots != nil {
		return sc.SteamRoots
	}
	var roots []string
	if p := steamPathFromRegistry(); p != "" {
		roots = append(roots, p)
	}
	home, _ := os.UserHomeDir()
	roots = append(roots,
		`C:\Program Files (x86)\Steam`,
		`C:\Program Files\Steam`,
		filepath.Join(home, ".local/share/Steam"),
		filepath.Join(home, ".steam/steam"),
		filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"),
	)
	return dedupePaths(roots)
}

// steamLibraryPaths resolves every Steam library folder: each root plus
// whatever its steamapps/libraryfolders.vdf points at (other drives).
func (sc *Scanner) steamLibraryPaths() []string {
	var libs []string
	for _, root := range sc.steamRootDirs() {
		if !dirExists(root) {
			continue
		}
		libs = append(libs, root)
		raw, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range vdfPathRe.FindAllStringSubmatch(string(raw), -1) {
			p := strings.ReplaceAll(m[1], `\\`, `\`)
			libs = append(libs, filepath.FromSlash(p))
		}
	}
	return dedupePaths(libs)
}

// steamInstalledApps parses appmanifest files across all libraries.
func steamInstalledApps(libraries []string) []steamApp {
	var apps []steamApp
	for _, lib := range libraries {
		steamapps := filepath.Join(lib, "steamapps")
		manifests, err := filepath.Glob(filepath.Join(steamapps, "appmanifest_*.acf"))
		if err != nil {
			continue
		}
		for _, mf := range manifests {
			raw, err := os.ReadFile(mf)
			if err != nil {
				continue
			}
			s := string(raw)
			app := steamApp{}
			if m := acfAppIDRe.FindStringSubmatch(s); m != nil {
				app.AppID = m[1]
			}
			if m := acfNameRe.FindStringSubmatch(s); m != nil {
				app.Name = m[1]
			}
			if m := acfInstallDirRe.FindStringSubmatch(s); m != nil {
				app.InstallDir = filepath.Join(steamapps, "common", m[1])
			}
			if app.AppID != "" && app.Name != "" {
				apps = append(apps, app)
			}
		}
	}
	return apps
}

// ueSavePathUnder finds the Unreal Engine save convention inside a game
// install folder: <dir>/Saved/SaveGames or <dir>/<Project>/Saved/SaveGames
// (one level deep — how cracked/portable UE games like Black Myth: Wukong
// keep saves next to the game instead of %LOCALAPPDATA%). Returns "" when
// absent or empty.
func ueSavePathUnder(dir string) string {
	if p := filepath.Join(dir, "Saved", "SaveGames"); dirNonEmpty(p) {
		return p
	}
	subs := listSubdirs(dir)
	if len(subs) > 100 {
		return "" // not a game folder shape; don't probe huge trees
	}
	for _, sub := range subs {
		if p := filepath.Join(dir, sub, "Saved", "SaveGames"); dirNonEmpty(p) {
			return p
		}
	}
	return ""
}

func dirNonEmpty(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) > 0
}

func dedupePaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		key := strings.ToLower(abs)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out
}
