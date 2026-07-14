//go:build windows

package presets

import (
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// steamPathFromRegistry reads Steam's install location from the registry —
// authoritative even when Steam lives outside Program Files.
func steamPathFromRegistry() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("SteamPath")
	if err != nil || v == "" {
		return ""
	}
	return filepath.FromSlash(v)
}
