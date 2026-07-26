//go:build !windows

package cliapp

import (
	"os"

	"golang.org/x/sys/unix"
)

// enableVirtualTerminal is a no-op outside Windows: ANSI escapes work in any
// terminal that reports itself as one, and the TTY check has already run.
func enableVirtualTerminal() bool { return true }

// terminalWidth returns the terminal width in columns, or 0 when it can't be
// determined.
func terminalWidth() int {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil {
		return 0
	}
	return int(ws.Col)
}
