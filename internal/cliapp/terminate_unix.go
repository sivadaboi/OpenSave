//go:build !windows

package cliapp

import (
	"os"
	"syscall"
)

// terminate asks the daemon to shut down cleanly, so it removes its address
// and pid files and closes the database properly.
func terminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
