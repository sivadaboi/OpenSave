//go:build !windows

package presets

// fixedDriveRoots is Windows-only. Elsewhere everything hangs off one tree and
// the preset paths already cover it; there are no drive letters to sweep.
func fixedDriveRoots() []string { return nil }
