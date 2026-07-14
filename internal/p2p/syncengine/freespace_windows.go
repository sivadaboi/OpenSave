//go:build windows

package syncengine

import "golang.org/x/sys/windows"

// availableDiskBytes returns the free space (available to this user) on the
// volume holding dir. ok is false when it can't be determined, in which
// case callers skip the free-space check rather than block a sync.
func diskFreeBytes(dir string) (bytes uint64, ok bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &totalFree); err != nil {
		return 0, false
	}
	return freeToCaller, true
}
