package presets

import (
	"golang.org/x/sys/windows"
)

// fixedDriveRoots returns the root of every FIXED drive — the internal disks.
//
// Deliberately not "every drive letter". Removable, optical and network
// drives are excluded because touching them is not free: an empty optical
// drive can spin up, and a mapped network share that is no longer reachable
// blocks for seconds per call. A scan that hangs on a disconnected share
// would be a worse bug than the one this exists to fix.
func fixedDriveRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var roots []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(p) == windows.DRIVE_FIXED {
			roots = append(roots, root)
		}
	}
	return roots
}
