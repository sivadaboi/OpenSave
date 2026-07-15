package presets

import (
	"fmt"
	"os"
	"path/filepath"
)

// Proton/Wine save detection for Linux (Steam Deck and desktop).
//
// Windows games run through Proton write their saves inside a per-game Wine
// prefix at <library>/steamapps/compatdata/<appid>/pfx/drive_c/users/
// steamuser/. Each prefix belongs to exactly one Steam AppID, so anything
// found inside is attributed to that game (and picks up its cover art).

// protonSaveRoots are the Windows save conventions, relative to a prefix's
// steamuser home, that hold game data worth offering.
var protonSaveRoots = []string{
	filepath.Join("AppData", "Roaming"),
	filepath.Join("AppData", "Local"),
	filepath.Join("AppData", "LocalLow"),
	filepath.Join("Documents", "My Games"),
	filepath.Join("Documents"),
	filepath.Join("Saved Games"),
}

// protonVendorSkip are folders inside a prefix that are Windows/Wine
// plumbing or ubiquitous middleware, never a game's own save.
var protonVendorSkip = map[string]bool{
	"microsoft": true, "windows": true, "wine": true, "temp": true,
	"packages": true, "programs": true, "connecteddevicesplatform": true,
	"comms": true, "d3dscache": true, "inputmethod": true, "vulkan": true,
	"crashdumps": true, "diagnostics": true, "history": true,
}

// scanProtonCompat walks every Steam library's compatdata prefixes and
// offers the game saves it finds. appNames resolves AppIDs to titles from
// installed-game manifests; unresolved ones fall back to the AppID and are
// upgraded later by resolveNames.
func (sc *Scanner) scanProtonCompat(libraries []string, seen map[string]bool, appNames map[string]string) []DiscoveredSave {
	var found []DiscoveredSave

	for _, lib := range libraries {
		compat := filepath.Join(lib, "steamapps", "compatdata")
		entries, err := os.ReadDir(compat)
		if err != nil {
			continue
		}
		for _, e := range entries {
			appID := e.Name()
			if !e.IsDir() || !isAppID(appID) {
				continue
			}
			steamUser := filepath.Join(compat, appID, "pfx", "drive_c", "users", "steamuser")
			if !dirExists(steamUser) {
				continue
			}

			gameName := appNames[appID]
			perGame := 0
			for _, root := range protonSaveRoots {
				rootPath := filepath.Join(steamUser, root)
				for _, sub := range listSubdirs(rootPath) {
					if protonVendorSkip[toLowerASCII(sub)] || looksLikeHexHash(sub) {
						continue
					}
					savePath := filepath.Join(rootPath, sub)
					abs, err := filepath.Abs(savePath)
					if err != nil || seen[abs] || !dirNonEmpty(abs) {
						continue
					}
					seen[abs] = true

					name := sub
					if gameName != "" {
						name = fmt.Sprintf("%s (%s)", gameName, sub)
					}
					found = append(found, DiscoveredSave{
						ID:       "proton-" + appID + "-" + sanitizeID(root) + "-" + sanitizeID(sub),
						Name:     name,
						Type:     "game",
						SavePath: savePath,
						AppID:    appID,
					})
					perGame++
					if perGame >= 12 { // a single prefix shouldn't flood the grid
						break
					}
				}
				if perGame >= 12 {
					break
				}
			}
		}
	}
	return found
}
