//go:build windows

package watcher

import (
	"errors"
	"os"
	"syscall"
)

const (
	errnoSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	errnoLockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION
)

// isFileLocked probes whether another process holds the file with an
// incompatible sharing mode (a game actively writing its save). The probe
// opens read-only: we only ever read saves to snapshot them, so write
// access is irrelevant — and crucially, a read-only or
// permission-restricted file must NOT register as locked, or the gameplay
// guard would poll it forever (a real bug in the original JS app, fixed
// here per its author's later walkthrough notes).
func isFileLocked(path string) bool {
	f, err := os.Open(path)
	if err == nil {
		f.Close()
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == errnoSharingViolation || errno == errnoLockViolation
	}
	return false
}
