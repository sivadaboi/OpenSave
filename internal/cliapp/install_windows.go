//go:build windows

package cliapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const installedName = "opensave.exe"

const pathActivationHint = "Open a new PowerShell window and run `opensave`."

func defaultInstallDir() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", fmt.Errorf("LOCALAPPDATA is not set — pass --dir to choose a location")
	}
	return filepath.Join(local, "OpenSave", "bin"), nil
}

// normalizePathEntry makes two spellings of the same directory compare equal:
// Windows paths are case-insensitive and a trailing separator is meaningless.
func normalizePathEntry(p string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(p), `\/`))
}

// writeAliases drops `os` and `opensave-cli` next to the binary as .cmd
// shims. Shims rather than copies of a 15 MB binary, and rather than
// symlinks, which need admin rights or Developer Mode.
func writeAliases(dir string) []string {
	var out []string
	for _, alias := range []string{"os", "opensave-cli"} {
		shim := filepath.Join(dir, alias+".cmd")
		body := "@echo off\r\n\"%~dp0" + installedName + "\" %*\r\n"
		if err := os.WriteFile(shim, []byte(body), 0o755); err == nil {
			out = append(out, shim)
		}
	}
	return out
}

// nextPathValue returns the PATH value that should replace current once dir
// is on it, or "" when dir is already there and nothing needs writing.
//
// Split out from the registry I/O so the part that can damage a user's PATH
// is testable without touching HKCU.
func nextPathValue(current, dir string) string {
	if pathContains(current, dir) {
		return ""
	}
	if strings.TrimSpace(current) == "" {
		return dir
	}
	return strings.TrimRight(current, ";") + ";" + dir
}

// ensureOnPath adds dir to the user's PATH, reporting whether it changed
// anything.
//
// Writes HKCU\Environment directly instead of going through
// SetEnvironmentVariable. That API returns the *expanded* value on read and
// writes back a plain REG_SZ, so a PATH containing %USERPROFILE% or
// %JAVA_HOME% gets those references silently baked into whatever they
// happened to resolve to at that moment — and then stops tracking the
// variable. Preserving the original value type is the difference between
// adding one entry and quietly rewriting the user's entire PATH.
func ensureOnPath(dir string) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer key.Close()

	current, valType, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return false, fmt.Errorf("read PATH: %w", err)
	}
	if err == registry.ErrNotExist {
		// No user PATH at all yet; REG_EXPAND_SZ is what Windows uses.
		valType = registry.EXPAND_SZ
		current = ""
	}
	updated := nextPathValue(current, dir)
	if updated == "" {
		return false, nil
	}

	// Write back with the type it already had, so unexpanded references keep
	// working. GetStringValue does not expand for us, so `current` still
	// holds the literal %VAR% text and round-trips intact.
	switch valType {
	case registry.EXPAND_SZ:
		err = key.SetExpandStringValue("Path", updated)
	default:
		err = key.SetStringValue("Path", updated)
	}
	if err != nil {
		return false, fmt.Errorf("write PATH: %w", err)
	}

	broadcastEnvironmentChange()

	// Also update this process, so a `opensave` invoked from the very shell
	// that ran the install resolves without reopening it.
	_ = os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
	return true, nil
}

// broadcastEnvironmentChange tells running programs the environment moved.
// Without it, Explorer (and anything it launches afterwards) keeps handing
// out the old PATH until the next sign-out. Already-open consoles never
// update regardless — they copied the environment when they started — which
// is why the hint still says to open a new window.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xFFFF
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	if err := proc.Find(); err != nil {
		return
	}
	var result uintptr
	// Best effort: a hung top-level window must not wedge the installer, so
	// this aborts rather than waiting on one.
	_, _, _ = proc.Call(
		uintptr(hwndBroadcast), uintptr(wmSettingChange), 0,
		uintptr(unsafe.Pointer(env)),
		uintptr(smtoAbortIfHung), uintptr(5000),
		uintptr(unsafe.Pointer(&result)),
	)
}
