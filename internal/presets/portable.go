package presets

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Portable emulator installs.
//
// The presets locate each emulator by its per-user data folder — RetroArch at
// %APPDATA%/RetroArch, Azahar at %APPDATA%/Azahar, and so on. That is where
// an installer puts them, and it is on C: whatever drive the emulator itself
// lives on.
//
// Emulators are very often not installed, though. RetroArch, the Citra forks
// and the yuzu forks all ship as a folder you unzip anywhere, and in that mode
// they keep their data next to the executable instead — so a user with
// "D:\Emulators\RetroArch" has no %APPDATA%/RetroArch at all, the scan finds
// nothing, and the app looks broken for exactly the audience most likely to
// have a dozen emulators. Reported by a user on Windows 11 whose emulators
// were all on a drive other than C:.
//
// Adding the folder as a custom scan path did not help either: that offers
// each immediate subfolder whole, so "RetroArch" came out as one save
// location covering the entire install — cores, BIOS, ROMs and all — which is
// worse than not finding it.
//
// The layout inside a portable install is the same as inside the data folder,
// just rooted differently: either directly under the install root
// (RetroArch's saves/ and states/) or under a "user" folder beside the
// executable (the Citra and yuzu lineages). So the tail of each preset path
// is reused, and a match requires BOTH the folder to be named after the
// emulator and that tail to actually exist — the tail alone is not evidence,
// since plenty of game folders contain something called "saves".

// portableUserDirs are the places a portable install keeps the data that an
// installed one would put in its per-user folder. "" means the install root
// itself.
var portableUserDirs = []string{"", "user"}

// appDataTail splits a Windows preset path of the form %APPDATA%/<App>/<tail>
// into the app's folder name and the part below it. Returns ok=false for
// presets that are not anchored at %APPDATA% (Documents, Saved Games, a
// wrapper root), which have no portable equivalent to look for.
func appDataTail(presetPath string) (appDir, tail string, ok bool) {
	const prefix = "%APPDATA%/"
	if !strings.HasPrefix(presetPath, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(presetPath, prefix)
	slash := strings.Index(rest, "/")
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", false // no tail: the app folder IS the save folder
	}
	return rest[:slash], rest[slash+1:], true
}

// looksLikeInstallOf reports whether a directory name plausibly names an
// install of the given emulator. Exact match, or the name with a suffix —
// unzipped builds arrive as "RetroArch-Win64" or "Azahar-2120" far more often
// than as a bare name.
func looksLikeInstallOf(dirName, appDir string) bool {
	d, a := toLowerASCII(dirName), toLowerASCII(appDir)
	if d == a {
		return true
	}
	if !strings.HasPrefix(d, a) {
		return false
	}
	// Require a separator after the name so "eden" does not match "edenring".
	switch d[len(a)] {
	case '-', '_', ' ', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	}
	return false
}

// emulatorContainerDirs are the folder names people keep emulators in. Looked
// inside one level deeper, because "D:\Emulators\RetroArch" is at least as
// common as "D:\RetroArch".
var emulatorContainerDirs = map[string]bool{
	"emulators": true, "emulation": true, "emus": true, "emu": true,
	"roms": true, "games": true,
}

// scanFixedDrivesForPortableEmulators looks for portable emulator installs
// sitting at the top of each internal drive, or one level inside a folder
// obviously meant to hold them.
//
// The reported complaint was that auto-scan found nothing, and telling
// somebody to go and configure a scan path is not the same as it working.
// This keeps the cost bounded and predictable: one directory listing per
// fixed drive, plus one more for each folder actually named like an emulator
// collection. It does not walk drives — a full sweep of a games drive would
// take minutes and read tens of thousands of directories for a handful of
// hits.
// driveRoots is swappable so tests can exercise the sweep against a temporary
// tree instead of whatever drives the machine running them happens to have.
var driveRoots = fixedDriveRoots

func scanFixedDrivesForPortableEmulators() []DiscoveredSave {
	var found []DiscoveredSave
	for _, root := range driveRoots() {
		for _, top := range listSubdirs(root) {
			dir := filepath.Join(root, top)
			if saves := portableEmulatorSaves(dir); len(saves) > 0 {
				found = append(found, saves...)
				continue
			}
			if !emulatorContainerDirs[toLowerASCII(top)] {
				continue
			}
			for _, sub := range listSubdirs(dir) {
				found = append(found, portableEmulatorSaves(filepath.Join(dir, sub))...)
			}
		}
	}
	return found
}

// portableEmulatorSaves returns the save locations of a portable emulator
// install rooted at dir, or nil if dir does not look like one.
func portableEmulatorSaves(dir string) []DiscoveredSave {
	name := filepath.Base(dir)
	var found []DiscoveredSave

	for _, p := range presetDefs {
		if p.Type != "emulator" || p.IsWrapper {
			continue
		}
		appDir, tail, ok := appDataTail(p.Path)
		if !ok || !looksLikeInstallOf(name, appDir) {
			continue
		}
		for _, userDir := range portableUserDirs {
			saveRoot := filepath.Join(dir, userDir, filepath.FromSlash(tail))
			if !dirExists(saveRoot) {
				continue
			}
			// Same reasoning as the installed case: offering the yuzu-lineage
			// NAND root would sync a per-install profile id, which the other
			// device's emulator does not recognise. Descend to the titles.
			// The NAME must be exactly what an installed copy of the same
			// emulator produces, because that is what the two devices match
			// on: tracking derives a game's id by slugifying its name, and a
			// peer resolves an incoming game by that id. Calling this one
			// "RetroArch Save Files (portable)" would make it
			// retroarch-save-files-portable here and retroarch-save-files on
			// a machine where RetroArch is installed normally — two ids that
			// never meet, so the saves this exists to sync would not.
			//
			// The discovery id still carries the distinction, which is what
			// keeps a portable and an installed copy on the SAME machine as
			// two separate entries in the scan results.
			if titles := switchNANDTitles(saveRoot); p.SwitchNAND && len(titles) > 0 {
				for _, title := range titles {
					found = append(found, DiscoveredSave{
						ID:       p.ID + "-portable-" + filepath.Base(title),
						Name:     fmt.Sprintf("%s - Title ID: %s", p.Name, filepath.Base(title)),
						Type:     p.Type,
						SavePath: title,
					})
				}
			} else {
				found = append(found, DiscoveredSave{
					ID:       p.ID + "-portable-" + sanitizeID(name),
					Name:     p.Name,
					Type:     p.Type,
					SavePath: saveRoot,
				})
			}
			break // one layout per preset; don't offer root/ and root/user/ both
		}
	}
	return found
}
