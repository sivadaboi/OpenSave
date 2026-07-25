//go:build windows

package cliapp

import "os"

// terminate stops the daemon. Windows has no SIGTERM for another process, so
// this is an outright kill; the daemon's own signal handling only covers
// Ctrl+C in its own console. Files it would have cleaned up (daemon.addr,
// daemon.pid) are handled by the caller and by the next start.
func terminate(proc *os.Process) error {
	return proc.Kill()
}
