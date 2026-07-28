//go:build !windows

package cliapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const installedName = "opensave"

const pathActivationHint = "Open a new terminal, or run `hash -r`, and `opensave` will resolve."

// defaultInstallDir follows the XDG user-binary convention. ~/.local/bin is
// already on PATH on most desktop distributions (and on SteamOS), so the
// common case needs no shell-config edit at all.
func defaultInstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func normalizePathEntry(p string) string {
	return strings.TrimRight(strings.TrimSpace(p), "/")
}

// writeAliases links `os` and `opensave-cli` to the installed binary.
// Symlinks are free here, unlike on Windows.
func writeAliases(dir string) []string {
	var out []string
	for _, alias := range []string{"os", "opensave-cli"} {
		link := filepath.Join(dir, alias)
		if _, err := os.Lstat(link); err == nil {
			_ = os.Remove(link)
		}
		if err := os.Symlink(filepath.Join(dir, installedName), link); err == nil {
			out = append(out, link)
		}
	}
	return out
}

// shellProfiles lists the startup files worth appending a PATH line to.
// Only files that already exist are touched: creating ~/.zshrc on a bash
// system, or a profile for a shell the user does not have, leaves clutter
// behind that nothing ever reads.
func shellProfiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range []string{".bashrc", ".zshrc", ".profile"} {
		p := filepath.Join(home, name)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// ensureOnPath appends a PATH line to the user's shell profiles when the
// directory isn't already reachable.
func ensureOnPath(dir string) (bool, error) {
	if pathContains(os.Getenv("PATH"), dir) {
		return false, nil
	}

	profiles := shellProfiles()
	if len(profiles) == 0 {
		return false, fmt.Errorf("found no shell profile to update (looked for ~/.bashrc, ~/.zshrc, ~/.profile)")
	}

	line := fmt.Sprintf("export PATH=\"%s:$PATH\"", dir)
	marker := "# added by opensave install"
	var wrote bool
	for _, p := range profiles {
		existing, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// Don't stack a duplicate export on every re-run.
		if strings.Contains(string(existing), line) {
			continue
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}
		_, err = fmt.Fprintf(f, "\n%s\n%s\n", marker, line)
		f.Close()
		if err == nil {
			wrote = true
		}
	}
	if !wrote {
		// Already present in every profile: PATH is configured, this shell
		// just hasn't re-read it.
		return false, nil
	}

	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return true, nil
}
