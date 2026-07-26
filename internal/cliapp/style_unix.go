//go:build !windows

package cliapp

// enableVirtualTerminal is a no-op outside Windows: ANSI escapes work in any
// terminal that reports itself as one, and the TTY check has already run.
func enableVirtualTerminal() bool { return true }
