package presets

import (
	"fmt"
	"os"
	"path/filepath"
)

// Wine/Proton prefixes that don't belong to Steam.
//
// Steam's own prefixes live under <library>/steamapps/compatdata and are
// handled by scanProtonCompat. Everything else — Heroic, Lutris, Bottles, a
// bare ~/.wine — is where non-Steam and cracked games end up on Linux and on
// a Steam Deck, and none of it was being scanned. A game launched through any
// of those had its saves sitting in a prefix OpenSave never looked inside.
//
// Kept bounded for the same reason as the rest of the scanner: only known
// launcher locations are visited, never a blind walk of $HOME.

// maxPrefixesPerLauncher stops a pathological library (hundreds of bottles)
// from dominating a scan.
const maxPrefixesPerLauncher = 60

// winePrefixCandidate is a prefix directory plus the launcher it came from,
// used to label results so the user can tell two copies apart.
type winePrefixCandidate struct {
	path     string
	launcher string
}

// winePrefixDirs returns the prefix directories for every non-Steam launcher
// present on this machine. Both native and Flatpak install locations are
// checked, since on a Deck these are almost always Flatpaks.
func (sc *Scanner) winePrefixDirs() []winePrefixCandidate {
	home := sc.linuxHome()
	if home == "" {
		return nil
	}

	// Each entry is a directory whose *children* are prefixes.
	prefixParents := []struct {
		dir      string
		launcher string
	}{
		// Heroic (Epic/GOG/Amazon, and any manually added game).
		{filepath.Join(home, "Games", "Heroic", "Prefixes"), "Heroic"},
		{filepath.Join(home, "Games", "Heroic", "Prefixes", "default"), "Heroic"},
		{filepath.Join(home, ".var", "app", "com.heroicgameslauncher.hgl", "config", "heroic", "Prefixes"), "Heroic"},
		{filepath.Join(home, ".var", "app", "com.heroicgameslauncher.hgl", "config", "heroic", "Prefixes", "default"), "Heroic"},

		// Bottles.
		{filepath.Join(home, ".var", "app", "com.usebottles.bottles", "data", "bottles", "bottles"), "Bottles"},
		{filepath.Join(home, ".local", "share", "bottles", "bottles"), "Bottles"},

		// Lutris keeps prefixes wherever the install script put them; these
		// are its defaults.
		{filepath.Join(home, "Games"), "Lutris"},
		{filepath.Join(home, ".var", "app", "net.lutris.Lutris", "data", "lutris", "prefixes"), "Lutris"},
		{filepath.Join(home, ".local", "share", "lutris", "prefixes"), "Lutris"},

		// Generic Wine.
		{filepath.Join(home, ".local", "share", "wineprefixes"), "Wine"},
	}

	var out []winePrefixCandidate
	for _, parent := range prefixParents {
		if !dirExists(parent.dir) {
			continue
		}
		n := 0
		for _, sub := range listSubdirs(parent.dir) {
			candidate := filepath.Join(parent.dir, sub)
			if !isWinePrefix(candidate) {
				continue
			}
			out = append(out, winePrefixCandidate{path: candidate, launcher: parent.launcher})
			if n++; n >= maxPrefixesPerLauncher {
				break
			}
		}
	}

	// A bare ~/.wine is itself a prefix, not a parent of prefixes.
	if def := filepath.Join(home, ".wine"); isWinePrefix(def) {
		out = append(out, winePrefixCandidate{path: def, launcher: "Wine"})
	}
	return out
}

// isWinePrefix reports whether a directory looks like a Wine prefix.
func isWinePrefix(dir string) bool {
	return dirExists(filepath.Join(dir, "drive_c"))
}

// prefixUserDirs returns the per-user home directories inside a prefix.
// Steam's prefixes always use "steamuser", but Heroic, Lutris and Bottles
// name it after the actual account — hardcoding steamuser is exactly why
// those prefixes yielded nothing.
func prefixUserDirs(prefix string) []string {
	usersDir := filepath.Join(prefix, "drive_c", "users")
	var out []string
	for _, user := range listSubdirs(usersDir) {
		if user == "Public" || user == "Default" || user == "Default User" || user == "All Users" {
			continue
		}
		out = append(out, filepath.Join(usersDir, user))
	}
	return out
}

// scanWinePrefixes walks non-Steam launcher prefixes and offers the saves
// inside, using the same conventions and noise filters as the Proton pass.
func (sc *Scanner) scanWinePrefixes(seen map[string]bool) []DiscoveredSave {
	if sc.goos() != "linux" {
		return nil
	}

	var found []DiscoveredSave
	for _, candidate := range sc.winePrefixDirs() {
		// The prefix folder name is usually the game name for Heroic and
		// Bottles, which is the best label available here.
		prefixName := filepath.Base(candidate.path)

		perPrefix := 0
		for _, userHome := range prefixUserDirs(candidate.path) {
			for _, root := range protonSaveRoots {
				rootPath := filepath.Join(userHome, root)
				for _, sub := range listSubdirs(rootPath) {
					if protonVendorSkip[toLowerASCII(sub)] || looksLikeHexHash(sub) || isCacheDirName(sub) {
						continue
					}
					savePath := filepath.Join(rootPath, sub)
					abs, err := filepath.Abs(savePath)
					if err != nil || seen[abs] || !dirNonEmpty(abs) {
						continue
					}
					// A precise Ludusavi hit inside this folder wins; the
					// broad parent would just be noise on top of it.
					if seenInside(seen, abs) {
						continue
					}
					seen[abs] = true

					found = append(found, DiscoveredSave{
						ID: "wine-" + sanitizeID(candidate.launcher) + "-" +
							sanitizeID(prefixName) + "-" + sanitizeID(sub),
						Name:     fmt.Sprintf("%s (%s)", sub, candidate.launcher),
						Type:     "game",
						SavePath: savePath,
					})
					if perPrefix++; perPrefix >= 12 {
						break
					}
				}
				if perPrefix >= 12 {
					break
				}
			}
			if perPrefix >= 12 {
				break
			}
		}
	}
	return found
}

// ensure os is referenced even if the helpers above change shape.
var _ = os.ReadDir
