//go:build windows

package cliapp

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal turns on ANSI escape handling for the console.
// Windows Terminal does this itself, but the legacy conhost does not — and
// without it every escape sequence prints as literal garbage like "←[38;2m".
// Returns false when it can't be enabled, so styling is skipped entirely
// rather than corrupting the output.
func enableVirtualTerminal() bool {
	const enableVirtualTerminalProcessing = 0x0004

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	handle := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	ret, _, _ := getConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if ret == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true // already on
	}
	ret, _, _ = setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	return ret != 0
}

// terminalWidth returns the console width in columns, or 0 when it can't be
// determined (piped output, or a handle that isn't a console).
func terminalWidth() int {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
		return 0
	}
	return int(info.Window.Right - info.Window.Left + 1)
}
