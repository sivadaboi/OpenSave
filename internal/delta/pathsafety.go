package delta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsSafePath reports whether the resolved absolute path of relPath, joined
// onto baseDir, stays inside baseDir — blocking path traversal (e.g.
// "../../etc/passwd") the same way delta.js's isSafePath() does.
func IsSafePath(baseDir, relPath string) bool {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(filepath.Join(base, relPath))
	if err != nil {
		return false
	}

	base = filepath.Clean(base)
	target = filepath.Clean(target)

	if target == base {
		return true
	}
	return strings.HasPrefix(target, base+string(os.PathSeparator))
}

// ResolveLocalSaveFilePath inspects a game's configured save path and
// reports whether it should be treated as a single-file save (isFile=true,
// in which case the manifest/watcher/patch logic operates on one file
// keyed by its base name) or a directory tree. A path that does not exist
// yet (game never launched) defaults to directory mode, since it will be
// created as a folder on first sync/snapshot the same way the JS app
// assumes a directory when it can't stat the configured path.
func ResolveLocalSaveFilePath(savePath string) (isFile bool, err error) {
	info, statErr := os.Stat(savePath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, fmt.Errorf("stat save path: %w", statErr)
	}
	return !info.IsDir(), nil
}
