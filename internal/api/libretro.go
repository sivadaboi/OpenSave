package api

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Box art for emulator saves comes from the libretro thumbnail collection,
// which is the same set RetroArch itself downloads. It needs no API key and
// no auth, which is why it is the first non-Steam art source here: adding it
// costs no credential to distribute and leaks nothing about a user's library
// to a server we run.
//
// The collection is addressed by two exact strings — the system's full name
// and the ROM's No-Intro/Redump filename. There is no search endpoint, so a
// lookup either knows both or it fails. Everything below exists to turn a
// save file's path into those two strings.
const libretroThumbBase = "https://thumbnails.libretro.com"

// libretroCoreSystems maps a RetroArch core name to the thumbnail system it
// plays. This is the reliable signal, but only when the user has enabled
// "Sort saves into folders by core name" — then a save lands in
// saves/<Core Name>/<Rom>.srm and the core names the system outright.
//
// Keys are lowercased core names as RetroArch writes them to disk; several
// cores map to one system because libretro ships multiple emulators per
// platform and users pick different ones.
var libretroCoreSystems = map[string]string{
	"snes9x":           "Nintendo - Super Nintendo Entertainment System",
	"bsnes":            "Nintendo - Super Nintendo Entertainment System",
	"bsnes-hd":         "Nintendo - Super Nintendo Entertainment System",
	"nestopia":         "Nintendo - Nintendo Entertainment System",
	"mesen":            "Nintendo - Nintendo Entertainment System",
	"fceumm":           "Nintendo - Nintendo Entertainment System",
	"quicknes":         "Nintendo - Nintendo Entertainment System",
	"gambatte":         "Nintendo - Game Boy Color",
	"sameboy":          "Nintendo - Game Boy Color",
	"tgb dual":         "Nintendo - Game Boy Color",
	"mgba":             "Nintendo - Game Boy Advance",
	"vba-m":            "Nintendo - Game Boy Advance",
	"vbam":             "Nintendo - Game Boy Advance",
	"gpsp":             "Nintendo - Game Boy Advance",
	"mupen64plus-next": "Nintendo - Nintendo 64",
	"parallel n64":     "Nintendo - Nintendo 64",
	"melonds":          "Nintendo - Nintendo DS",
	"desmume":          "Nintendo - Nintendo DS",
	"citra":            "Nintendo - Nintendo 3DS",
	"dolphin":          "Nintendo - GameCube",
	"genesis plus gx":  "Sega - Mega Drive - Genesis",
	"picodrive":        "Sega - Mega Drive - Genesis",
	"blastem":          "Sega - Mega Drive - Genesis",
	"beetle saturn":    "Sega - Saturn",
	"yabasanshiro":     "Sega - Saturn",
	"flycast":          "Sega - Dreamcast",
	"beetle psx":       "Sony - PlayStation",
	"beetle psx hw":    "Sony - PlayStation",
	"pcsx-rearmed":     "Sony - PlayStation",
	"swanstation":      "Sony - PlayStation",
	"duckstation":      "Sony - PlayStation",
	"ppsspp":           "Sony - PlayStation Portable",
	"beetle pce":       "NEC - PC Engine - TurboGrafx 16",
	"beetle pce fast":  "NEC - PC Engine - TurboGrafx 16",
	"stella":           "Atari - 2600",
	"mame":             "MAME",
	"fbneo":            "FBNeo - Arcade Games",
}

// libretroExtSystems is the fallback when no core folder is present, which is
// RetroArch's default layout: saves/ is flat and the extension is all there
// is to go on. One extension spans several systems — .srm is battery-backed
// SRAM for anything with a battery — so these are candidates to try in order,
// most common first, not an answer.
//
// Trying 4-5 URLs per file is affordable because a miss is cached (see
// coverMisses) and only ever paid once per file per TTL.
var libretroExtSystems = map[string][]string{
	".srm": {
		"Nintendo - Super Nintendo Entertainment System",
		"Nintendo - Game Boy Advance",
		"Nintendo - Game Boy Color",
		"Nintendo - Nintendo Entertainment System",
		"Sega - Mega Drive - Genesis",
	},
	".sav": {
		"Nintendo - Game Boy Advance",
		"Nintendo - Game Boy Color",
		"Nintendo - Nintendo DS",
	},
	".dsv": {"Nintendo - Nintendo DS"},
	".mcr": {"Sony - PlayStation"},
	".mcd": {"Sony - PlayStation"},
	".eep": {"Nintendo - Nintendo 64"},
	".fla": {"Nintendo - Nintendo 64"},
	".mpk": {"Nintendo - Nintendo 64"},
}

// libretroSystemSet is every system name this file can produce, used to keep
// a caller from putting an arbitrary string into a URL path. cover.go's
// isNumericID guard exists because App IDs are the only thing that reaches
// the Steam CDN template; the equivalent guarantee here is that the system
// segment is always one of these constants, never user input.
var libretroSystemSet = func() map[string]bool {
	set := map[string]bool{}
	for _, s := range libretroCoreSystems {
		set[s] = true
	}
	for _, cands := range libretroExtSystems {
		for _, s := range cands {
			set[s] = true
		}
	}
	return set
}()

// libretroBadChars are the characters the thumbnail collection replaces with
// "_" when it names a file. A ROM called "Pokemon: Red/Blue" is stored as
// "Pokemon_ Red_Blue.png", so a lookup has to apply the same substitution or
// every title with punctuation misses.
const libretroBadChars = `&*/:` + "`" + `<>?\|"`

// libretroRomName turns a save file's base name into the name the thumbnail
// collection would file it under: extension dropped, libretro's forbidden
// characters substituted.
//
// It deliberately does not strip the region tag — "(USA)" is part of the
// No-Intro name and removing it would turn an exact match into a miss.
func libretroRomName(fileName string) string {
	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	// RetroArch appends a slot number to states ("Game.state1"); the base
	// name still ends in ".state" after one Ext strip.
	if ext := filepath.Ext(name); strings.HasPrefix(strings.ToLower(ext), ".state") {
		name = strings.TrimSuffix(name, ext)
	}
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(libretroBadChars, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// libretroCandidates returns the systems worth trying for one save file,
// given its path relative to the emulator's save root. A core-name folder
// wins outright; otherwise the extension supplies an ordered guess list.
func libretroCandidates(relPath string) []string {
	if dir := filepath.Dir(relPath); dir != "." && dir != string(filepath.Separator) {
		// Only the segment directly above the file can be a core name;
		// deeper nesting is the user's own foldering.
		core := strings.ToLower(filepath.Base(dir))
		if sys, ok := libretroCoreSystems[core]; ok {
			return []string{sys}
		}
	}
	return libretroExtSystems[strings.ToLower(filepath.Ext(relPath))]
}

// libretroThumbURL builds the boxart URL for one system/ROM pair. It returns
// "" for a system outside libretroSystemSet, so a caller cannot construct a
// request to an arbitrary path even if a core map entry is later mistyped.
//
// Both segments are path-escaped: system names contain spaces, and ROM names
// contain spaces, parentheses and commas.
func libretroThumbURL(system, rom string) string {
	if !libretroSystemSet[system] || rom == "" {
		return ""
	}
	return libretroThumbBase + "/" + url.PathEscape(system) +
		"/Named_Boxarts/" + url.PathEscape(rom+".png")
}
