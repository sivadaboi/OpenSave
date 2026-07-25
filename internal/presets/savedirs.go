package presets

import (
	"path/filepath"
	"strings"
)

const (
	// saveScanMaxDepth is how many levels below a game's install root the
	// save-folder heuristic looks. Deep enough for the layouts games actually
	// use (<install>/savegames/<id>, <install>/Binaries/Saves), shallow enough
	// that a scan never walks a whole game tree.
	saveScanMaxDepth = 3
	// saveScanMaxFanout skips directories with an implausible number of
	// subfolders — asset dumps, not a place saves live.
	saveScanMaxFanout = 120
	// saveScanMaxHits caps how many save folders one install can contribute,
	// so a pathological tree can't flood the results.
	saveScanMaxHits = 4
)

// looksLikeSaveDirName reports whether a folder name suggests save data:
// "Saves", "savegames", "SaveData", "AutoSave", "SAVE" and friends. Matching
// ignores case and the separators people use interchangeably.
func looksLikeSaveDirName(name string) bool {
	return strings.Contains(normalizeDirName(name), "save")
}

// findSaveDirsUnder locates save folders inside a game's install directory by
// name, for games that keep saves next to the game rather than in a known
// per-engine or per-launcher location — e.g. Ubisoft titles storing them in
// <install>/savegames/<numeric id>, which no preset can enumerate.
//
// It is deliberately conservative: bounded depth and fan-out, cache folders
// skipped, empty folders ignored, and it does not descend past a match (so a
// save folder's own subfolders don't each become separate entries). When a
// matching folder contains a more specific matching child — the Unreal
// "Saved/SaveGames" shape — the child wins.
func findSaveDirsUnder(root string) []string {
	if root == "" || !dirExists(root) {
		return nil
	}
	var out []string

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > saveScanMaxDepth || len(out) >= saveScanMaxHits {
			return
		}
		subs := listSubdirs(dir)
		if len(subs) > saveScanMaxFanout {
			return
		}
		for _, sub := range subs {
			if len(out) >= saveScanMaxHits {
				return
			}
			if isCacheDirName(sub) {
				continue
			}
			full := filepath.Join(dir, sub)
			if looksLikeSaveDirName(sub) {
				out = append(out, resolveSaveDir(full)...)
				continue // never descend past a match
			}
			walk(full, depth+1)
		}
	}
	walk(root, 1)
	return out
}

// resolveSaveDir picks what to offer for a folder whose name matched. A
// container like "Saved" that holds a more specific "SaveGames" resolves to
// the child; otherwise the folder itself is used when it actually holds data.
func resolveSaveDir(dir string) []string {
	var deeper []string
	for _, sub := range listSubdirs(dir) {
		if isCacheDirName(sub) {
			continue
		}
		if looksLikeSaveDirName(sub) {
			child := filepath.Join(dir, sub)
			if dirNonEmpty(child) {
				deeper = append(deeper, child)
			}
		}
	}
	if len(deeper) > 0 {
		return deeper
	}
	if dirNonEmpty(dir) {
		return []string{dir}
	}
	return nil
}
