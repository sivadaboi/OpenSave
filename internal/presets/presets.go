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
	Path      string   // Windows path, with %VAR% placeholders
	LinuxPath []string // Linux candidates (~ and XDG-relative); empty = not on Linux
	IsWrapper bool     // wrapper dirs hold one subfolder per game/AppID
}

var presetDefs = []preset{
	// Famous emulators. Linux paths cover native installs and the common
	// Flatpak sandbox locations.
	{ID: "ryujinx", Name: "Ryujinx Switch Emulator", Type: "emulator", Path: "%APPDATA%/Ryujinx/bis/user/save",
		LinuxPath: []string{"~/.config/Ryujinx/bis/user/save", "~/.var/app/org.ryujinx.Ryujinx/config/Ryujinx/bis/user/save"}},
	{ID: "yuzu", Name: "Yuzu Switch Emulator", Type: "emulator", Path: "%APPDATA%/yuzu/nand/user/save",
		LinuxPath: []string{"~/.local/share/yuzu/nand/user/save", "~/.var/app/org.yuzu_emu.yuzu/data/yuzu/nand/user/save"}},
	{ID: "citra", Name: "Citra 3DS Emulator", Type: "emulator", Path: "%APPDATA%/Citra/sdmc/Nintendo 3DS",
		LinuxPath: []string{"~/.local/share/citra-emu/sdmc/Nintendo 3DS", "~/.var/app/org.citra_emu.citra/data/citra-emu/sdmc/Nintendo 3DS"}},
	{ID: "dolphin", Name: "Dolphin GameCube/Wii Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Dolphin Emulator",
		LinuxPath: []string{"~/.local/share/dolphin-emu", "~/.var/app/org.DolphinEmu.dolphin-emu/data/dolphin-emu"}},
	{ID: "pcsx2", Name: "PCSX2 PS2 Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/PCSX2/memcards",
		LinuxPath: []string{"~/.config/PCSX2/memcards", "~/.var/app/net.pcsx2.PCSX2/config/PCSX2/memcards"}},
	{ID: "rpcs3", Name: "RPCS3 PS3 Emulator", Type: "emulator", Path: "%APPDATA%/rpcs3/dev_hdd0/home/00000001/savedata",
		LinuxPath: []string{"~/.config/rpcs3/dev_hdd0/home/00000001/savedata", "~/.var/app/net.rpcs3.RPCS3/config/rpcs3/dev_hdd0/home/00000001/savedata"}},
	{ID: "cemu", Name: "Cemu Wii U Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Cemu/mlc01/usr/save",
		LinuxPath: []string{"~/.local/share/Cemu/mlc01/usr/save", "~/.var/app/info.cemu.Cemu/data/Cemu/mlc01/usr/save"}},
	{ID: "ppsspp", Name: "PPSSPP PSP Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/PPSSPP/PSP/SAVEDATA",
		LinuxPath: []string{"~/.config/ppsspp/PSP/SAVEDATA", "~/.var/app/org.ppsspp.PPSSPP/config/ppsspp/PSP/SAVEDATA"}},
	{ID: "xenia", Name: "Xenia Xbox 360 Emulator", Type: "emulator", Path: "%USERPROFILE%/Documents/Xenia/content"}, // Windows-only emulator
	{ID: "retroarch-states", Name: "RetroArch Save States", Type: "emulator", Path: "%APPDATA%/RetroArch/states",
		LinuxPath: []string{"~/.config/retroarch/states", "~/.var/app/org.libretro.RetroArch/config/retroarch/states"}},
	{ID: "retroarch-saves", Name: "RetroArch Save Files", Type: "emulator", Path: "%APPDATA%/RetroArch/saves",
		LinuxPath: []string{"~/.config/retroarch/saves", "~/.var/app/org.libretro.RetroArch/config/retroarch/saves"}},

	// EmuDeck (Steam Deck & co.) relocates every emulator's saves into one
	// tree: Emulation/saves/<emulator>. Internal storage plus SD card
	// (SteamOS mounts cards under /run/media/<dev> or /run/media/deck/<label>).
	{ID: "emudeck", Name: "EmuDeck", Type: "emulator", IsWrapper: true,
		LinuxPath: []string{
			"~/Emulation/saves",
			"/run/media/*/Emulation/saves",
			"/run/media/*/*/Emulation/saves",
		}},

	// Steam-emulator / repack wrappers (each subfolder = one game). These
	// are Windows conventions; on Linux the same games run under Proton and
	// their saves live in the compatdata prefix (see scanProtonCompat).
	{ID: "goldberg", Name: "Goldberg Steam Emulator", Type: "repack", Path: "%APPDATA%/Goldberg SteamEmu Saves", IsWrapper: true},
	{ID: "gse", Name: "Goldberg (GSE fork) Saves", Type: "repack", Path: "%APPDATA%/GSE Saves", IsWrapper: true},
	{ID: "codex", Name: "CODEX / PLAZA Steam Emulator", Type: "repack", Path: "%PUBLIC%/Documents/Steam/CODEX", IsWrapper: true},
	{ID: "rune", Name: "RUNE Steam Emulator", Type: "repack", Path: "%PUBLIC%/Documents/Steam/RUNE", IsWrapper: true},
	{ID: "tenoke", Name: "Tenoke Steam Emulator", Type: "repack", Path: "%USERPROFILE%/Documents/Steam/TENOKE", IsWrapper: true},
	{ID: "empress", Name: "EMPRESS Saves", Type: "repack", Path: "%PUBLIC%/Documents/EMPRESS", IsWrapper: true},
	{ID: "onlinefix", Name: "Online-Fix Saves", Type: "repack", Path: "%PUBLIC%/Documents/OnlineFix", IsWrapper: true},
	{ID: "cpy", Name: "CPY Saves", Type: "repack", Path: "%PUBLIC%/Documents/CPY_SAVES", IsWrapper: true},
	{ID: "sse", Name: "SmartSteamEmu Saves", Type: "repack", Path: "%APPDATA%/SmartSteamEmu", IsWrapper: true},
	{ID: "skidrow-appdata", Name: "SKIDROW Saves", Type: "repack", Path: "%APPDATA%/SKIDROW", IsWrapper: true},
	{ID: "skidrow-local", Name: "SKIDROW Saves (Local)", Type: "repack", Path: "%LOCALAPPDATA%/SKIDROW", IsWrapper: true},
	{ID: "3dm", Name: "3DM Saves", Type: "repack", Path: "%PUBLIC%/Documents/3DMGAME", IsWrapper: true},
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

// resolveLinuxPath expands a Linux-style path template: a leading "~"
// becomes the home directory, and $XDG_DATA_HOME / $XDG_CONFIG_HOME are
// honored (falling back to their spec defaults). homeOverride lets tests
// point "~" at a temp dir.
func resolveLinuxPath(p, homeOverride string) string {
	home := homeOverride
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	p = filepath.ToSlash(p)
	// XDG bases, so ~/.local/share and ~/.config move with the env.
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = home + "/.local/share"
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = home + "/.config"
	}
	switch {
	case strings.HasPrefix(p, "~/.local/share"):
		p = dataHome + strings.TrimPrefix(p, "~/.local/share")
	case strings.HasPrefix(p, "~/.config"):
		p = configHome + strings.TrimPrefix(p, "~/.config")
	case strings.HasPrefix(p, "~/"):
		p = home + strings.TrimPrefix(p, "~")
	case p == "~":
		p = home
	}
	return filepath.Clean(filepath.FromSlash(p))
}

// resolvedPaths returns the existing save locations this preset maps to on
// the scanner's target OS (Windows: the single %VAR% path; Linux: each
// candidate that exists). Non-existent paths are dropped by the caller.
func (p preset) resolvedPaths(sc *Scanner) []string {
	if sc.goos() == "windows" {
		if p.Path == "" {
			return nil // Linux-only preset (EmuDeck)
		}
		return []string{ResolvePath(p.Path)}
	}
	// Non-Windows: only presets with Linux locations apply. A location may
	// contain wildcards (SD-card mount points vary per device/label).
	var out []string
	for _, lp := range p.LinuxPath {
		resolved := resolveLinuxPath(lp, sc.linuxHome())
		if strings.ContainsAny(resolved, "*?[") {
			if matches, err := filepath.Glob(resolved); err == nil {
				out = append(out, matches...)
			}
			continue
		}
		out = append(out, resolved)
	}
	return out
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
