package presets

import (
	"path/filepath"
	"runtime"
	"strings"
)

// FilterExcluded drops every discovered save that sits at or under one of the
// user's excluded directories. Excluding a folder is how a user tells the
// scanner to stop offering saves it shouldn't track — e.g. a stale
// "GSE saves" directory left behind after moving games into Steam.
//
// Matching is boundary-aware (excluding "…/Games" skips "…/Games/X" but not
// "…/GamesOther") and case-insensitive on Windows and macOS, mirroring those
// filesystems' own path semantics.
func FilterExcluded(saves []DiscoveredSave, excludePaths []string) []DiscoveredSave {
	if len(saves) == 0 || len(excludePaths) == 0 {
		return saves
	}
	roots := make([]string, 0, len(excludePaths))
	for _, ex := range excludePaths {
		if c := cleanForMatch(ex); c != "" {
			roots = append(roots, c)
		}
	}
	if len(roots) == 0 {
		return saves
	}
	out := make([]DiscoveredSave, 0, len(saves))
	for _, s := range saves {
		if !pathUnderAny(s.SavePath, roots) {
			out = append(out, s)
		}
	}
	return out
}

// pathUnderAny reports whether target is at or below any of the roots, which
// must already be normalized with cleanForMatch.
func pathUnderAny(target string, roots []string) bool {
	t := cleanForMatch(target)
	if t == "" {
		return false
	}
	sep := string(filepath.Separator)
	for _, r := range roots {
		if t == r || strings.HasPrefix(t, r+sep) {
			return true
		}
	}
	return false
}

// cleanForMatch normalizes a path for comparison: filepath.Clean plus
// case-folding on case-insensitive platforms.
func cleanForMatch(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}
