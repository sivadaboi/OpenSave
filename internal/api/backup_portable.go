package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Portable save paths for .sscb exports. Absolute paths are user- and
// machine-specific (C:\Users\alice\… means nothing on bob's PC), so the
// export manifest stores each save path with its user-profile prefix
// replaced by a token (%USERPROFILE%-style on Windows, $HOME-style on
// Linux) that the importing machine expands to its own locations. Paths
// outside every known root (D:\Games\…) pass through unchanged — they
// are already machine-portable in the way that matters.

// pathToken is one substitutable root, in match-priority order (most
// specific expansion first, so AppData\Local wins over the plain home).
type pathToken struct {
	token string
	dir   string
}

func portableTokens() []pathToken {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		envOr := func(env, fallback string) string {
			if v := os.Getenv(env); v != "" {
				return v
			}
			return fallback
		}
		return []pathToken{
			{"%LOCALAPPDATA%", envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))},
			{"%APPDATA%", envOr("APPDATA", filepath.Join(home, "AppData", "Roaming"))},
			{"%USERPROFILE%", home},
			{"%PUBLIC%", os.Getenv("PUBLIC")},
			{"%PROGRAMDATA%", os.Getenv("PROGRAMDATA")},
		}
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return []pathToken{
		{"$XDG_DATA_HOME", dataHome},
		{"$XDG_CONFIG_HOME", configHome},
		{"$HOME", home},
	}
}

// hasPathPrefix reports whether p lives under root (or is root), with
// Windows-appropriate case folding.
func hasPathPrefix(p, root string) bool {
	if root == "" {
		return false
	}
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
		root = strings.ToLower(root)
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// tokenizeSavePath returns the path with its longest matching known root
// replaced by that root's token, using forward slashes so the manifest is
// OS-neutral to parse. Unmatched paths come back unchanged (slashed).
func tokenizeSavePath(p string) string {
	clean := filepath.Clean(p)
	for _, t := range portableTokens() {
		if hasPathPrefix(clean, t.dir) {
			rel := clean[len(filepath.Clean(t.dir)):]
			return t.token + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(clean)
}

// resolvePortablePath expands a tokenized manifest path against this
// machine's locations. Returns "" when the path carries a token this OS
// doesn't know (a Windows token on Linux, or vice versa) — the caller
// must then skip the restore rather than guess.
func resolvePortablePath(portable string) string {
	if portable == "" {
		return ""
	}
	native := filepath.FromSlash(portable)
	if !strings.HasPrefix(portable, "%") && !strings.HasPrefix(portable, "$") {
		return filepath.Clean(native)
	}
	for _, t := range portableTokens() {
		if t.dir == "" {
			continue
		}
		if strings.HasPrefix(portable, t.token+"/") || portable == t.token {
			return filepath.Clean(filepath.Join(t.dir, filepath.FromSlash(strings.TrimPrefix(portable, t.token))))
		}
	}
	return ""
}
