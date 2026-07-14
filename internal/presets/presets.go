// Package presets auto-detects game save locations: emulator save dirs,
// Steam-emulator/repack wrapper folders, Steam userdata, Epic/GOG/Unreal
// conventions, and user-configured custom scan paths — porting
// src/daemon/presets.js.
package presets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DiscoveredSave is one auto-detected save location, offered to the user
// for tracking.
type DiscoveredSave struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // "emulator" | "repack" | "game"
	SavePath string `json:"savePath"`
	AppID    string `json:"appId,omitempty"`
}

type preset struct {
	ID        string
	Name      string
	Type      string
	Path      string // with %VAR% placeholders
	IsWrapper bool   // wrapper dirs hold one subfolder per game/AppID
}

var presetDefs = []preset{
	// Famous emulators
	{ID: "ryujinx", Name: "Ryujinx Switch Emulator", Type: "emulator", Path: "%APPDATA%/Ryujinx/bis/user/save"},
	{ID: "yuzu", Name: "Yuzu Switch Emulator", Type: "emulator", Path: "%APPDATA%/yuzu/nand/user/save"},
	{ID: "citra", Name: "Citra 3DS Emulator", Type: "emulator", Path: "%APPDATA%/Citra/sdmc/Nintendo 3DS"},
	{ID: "dolphin", Name: "Dolphin GameCube/Wii Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Dolphin Emulator"},
	{ID: "pcsx2", Name: "PCSX2 PS2 Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/PCSX2/memcards"},
	{ID: "rpcs3", Name: "RPCS3 PS3 Emulator", Type: "emulator", Path: "%APPDATA%/rpcs3/dev_hdd0/home/00000001/savedata"},
	{ID: "cemu", Name: "Cemu Wii U Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Cemu/mlc01/usr/save"},
	{ID: "ppsspp", Name: "PPSSPP PSP Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/PPSSPP/PSP/SAVEDATA"},
	{ID: "xenia", Name: "Xenia Xbox 360 Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Xenia/content"},
	{ID: "retroarch-states", Name: "RetroArch Save States", Type: "emulator", Path: "%APPDATA%/RetroArch/states"},
	{ID: "retroarch-saves", Name: "RetroArch Save Files", Type: "emulator", Path: "%APPDATA%/RetroArch/saves"},

	// Steam-emulator / repack wrappers (each subfolder = one game)
	{ID: "goldberg", Name: "Goldberg Steam Emulator", Type: "repack", Path: "%APPDATA%/Goldberg SteamEmu Saves", IsWrapper: true},
	{ID: "codex", Name: "CODEX / PLAZA Steam Emulator", Type: "repack", Path: "%PUBLIC%/Documents/Steam/CODEX", IsWrapper: true},
	{ID: "rune", Name: "RUNE Steam Emulator", Type: "repack", Path: "%PUBLIC%/Documents/Steam/RUNE", IsWrapper: true},
	{ID: "tenoke", Name: "Tenoke Steam Emulator", Type: "repack", Path: "%USERPROFILE%/Documents/Steam/TENOKE", IsWrapper: true},
	{ID: "flt", Name: "Fairlight (FLT) Saves", Type: "repack", Path: "%APPDATA%/FLT", IsWrapper: true},
	{ID: "ali", Name: "ALi Saves", Type: "repack", Path: "%APPDATA%/ALi", IsWrapper: true},
	{ID: "reloaded", Name: "RELOADED (RLD!) Wrapper", Type: "repack", Path: "%PROGRAMDATA%/Steam/RLD!", IsWrapper: true},
	{ID: "generic-wrapper", Name: "Public Documents Steam Wrapper", Type: "repack", Path: "%PUBLIC%/Documents/Steam", IsWrapper: true},
}

// wrapper subfolders that are Steam-emulator config/system dirs, not games
var wrapperSystemDirs = map[string]bool{
	"settings": true, "remote": true, "saves": true, "stats": true, "storage": true,
}

var envVarRe = regexp.MustCompile(`(?i)%(APPDATA|USERPROFILE|LOCALAPPDATA|PROGRAMDATA|PUBLIC)%`)

// ResolvePath substitutes Windows-style %VAR% placeholders using the
// environment (with the same fallbacks the JS version used) and returns an
// absolute path.
func ResolvePath(p string) string {
	home, _ := os.UserHomeDir()
	resolved := envVarRe.ReplaceAllStringFunc(p, func(match string) string {
		switch strings.ToUpper(strings.Trim(match, "%")) {
		case "APPDATA":
			if v := os.Getenv("APPDATA"); v != "" {
				return v
			}
			return filepath.Join(home, "AppData", "Roaming")
		case "USERPROFILE":
			return home
		case "LOCALAPPDATA":
			if v := os.Getenv("LOCALAPPDATA"); v != "" {
				return v
			}
			return filepath.Join(home, "AppData", "Local")
		case "PROGRAMDATA":
			if v := os.Getenv("PROGRAMDATA"); v != "" {
				return v
			}
			return `C:\ProgramData`
		case "PUBLIC":
			if v := os.Getenv("PUBLIC"); v != "" {
				return v
			}
			return `C:\Users\Public`
		}
		return match
	})
	abs, err := filepath.Abs(filepath.FromSlash(resolved))
	if err != nil {
		return resolved
	}
	return abs
}

var idSanitizeRe = regexp.MustCompile(`[^a-z0-9]`)

func sanitizeID(s string) string {
	return idSanitizeRe.ReplaceAllString(strings.ToLower(s), "-")
}

var digitsOnlyRe = regexp.MustCompile(`^\d+$`)

func isAppID(s string) bool {
	return digitsOnlyRe.MatchString(s)
}

func listSubdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	return subdirs
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
