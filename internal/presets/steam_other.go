//go:build !windows

package presets

// steamPathFromRegistry is Windows-only; other platforms rely on the
// well-known home-relative Steam locations.
func steamPathFromRegistry() string { return "" }
