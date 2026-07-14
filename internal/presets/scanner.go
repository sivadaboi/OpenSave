package presets

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Scanner performs the full auto-detection sweep. Zero value is not
// usable; construct with NewScanner.
type Scanner struct {
	// CacheFile is where resolved Steam AppID names persist across runs
	// (~/.opensave/steam-app-cache.json).
	CacheFile string
	// ResolveAppName resolves a Steam AppID to a display name (network
	// call in production, injectable for tests). Empty string = not found.
	ResolveAppName func(appID string) string
	// SteamUserdataPaths overrides the default Steam install locations
	// when non-nil (tests use this to stay hermetic on machines that have
	// a real Steam library).
	SteamUserdataPaths []string
	// SteamRoots overrides Steam install-root detection (registry +
	// well-known paths) when non-nil. Tests only.
	SteamRoots []string
	// LocalLowDir overrides %USERPROFILE%\AppData\LocalLow when non-empty.
	// Tests only.
	LocalLowDir string
}

// NewScanner builds a production Scanner using the Steam Store API
// resolver.
func NewScanner(cacheFile string) *Scanner {
	return &Scanner{
		CacheFile:      cacheFile,
		ResolveAppName: fetchSteamAppName,
	}
}

// Scan sweeps every known save convention and returns the discovered
// locations with names resolved as well as possible (offline dictionary
// -> disk cache -> Steam Store API).
func (sc *Scanner) Scan(customScanPaths []string) []DiscoveredSave {
	var discovered []DiscoveredSave

	// 1 & 2. Emulator presets and repack wrapper folders.
	for _, p := range presetDefs {
		resolved := ResolvePath(p.Path)
		if !dirExists(resolved) {
			continue
		}
		if p.IsWrapper {
			for _, sub := range listSubdirs(resolved) {
				if wrapperSystemDirs[toLowerASCII(sub)] {
					continue
				}
				d := DiscoveredSave{
					ID:       p.ID + "-" + sub,
					Name:     fmt.Sprintf("%s - Game ID: %s", p.Name, sub),
					Type:     p.Type,
					SavePath: filepath.Join(resolved, sub),
				}
				if isAppID(sub) {
					d.AppID = sub
				}
				discovered = append(discovered, d)
			}
		} else {
			discovered = append(discovered, DiscoveredSave{
				ID:       p.ID,
				Name:     p.Name,
				Type:     p.Type,
				SavePath: resolved,
			})
		}
	}

	// 3. Steam libraries: follow libraryfolders.vdf to every library drive
	// and parse appmanifests — exact installed-game names + AppIDs, plus
	// detection of games whose Steam library isn't on C:.
	libraries := sc.steamLibraryPaths()
	apps := steamInstalledApps(libraries)
	appNames := make(map[string]string, len(apps))
	for _, a := range apps {
		appNames[a.AppID] = a.Name
	}

	// 3a. Steam userdata folders (Windows + Linux/SteamOS/Flatpak).
	discovered = append(discovered, sc.scanSteamUserdata(dedupSet(discovered), appNames)...)

	// 3b. Saves kept inside install folders (the UE <Project>/Saved/
	// SaveGames convention): installed Steam games first, then any game
	// folder sitting next to steamapps on a library drive — which is where
	// portable/cracked installs like "D:\Games\Black Myth - Wukong" live.
	seen := dedupSet(discovered)
	for _, a := range apps {
		p := ueSavePathUnder(a.InstallDir)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		discovered = append(discovered, DiscoveredSave{
			ID: "steamlib-" + a.AppID, Name: a.Name, Type: "game",
			SavePath: p, AppID: a.AppID,
		})
	}
	for _, lib := range libraries {
		for _, sub := range listSubdirs(lib) {
			if libSystemDirs[toLowerASCII(sub)] {
				continue
			}
			p := ueSavePathUnder(filepath.Join(lib, sub))
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			discovered = append(discovered, DiscoveredSave{
				ID: "installdir-" + sanitizeID(sub), Name: sub, Type: "game",
				SavePath: p,
			})
		}
	}

	// 3c. Unity saves: %USERPROFILE%/AppData/LocalLow/<Company>/<Game> —
	// PICO PARK, Hollow Knight, Cuphead, and most Unity titles live here.
	discovered = append(discovered, sc.scanLocalLow()...)

	// 4. Epic "Saved Games" and GOG "My Games" wrapper folders.
	for _, w := range []struct{ id, name, path string }{
		{"epic-savedgames", "Epic / Saved Games", "%USERPROFILE%/Saved Games"},
		{"gog-mygames", "GOG / My Games", "%USERPROFILE%/Documents/My Games"},
	} {
		resolved := ResolvePath(w.path)
		if !dirExists(resolved) {
			continue
		}
		for _, sub := range listSubdirs(resolved) {
			discovered = append(discovered, DiscoveredSave{
				ID:       w.id + "-" + sanitizeID(sub),
				Name:     sub,
				Type:     "game",
				SavePath: filepath.Join(resolved, sub),
			})
		}
	}

	// 5. Unreal Engine saves: %LOCALAPPDATA%/<Game>/Saved/SaveGames.
	localAppData := ResolvePath("%LOCALAPPDATA%")
	for _, dir := range listSubdirs(localAppData) {
		saveGames := filepath.Join(localAppData, dir, "Saved", "SaveGames")
		if !dirExists(saveGames) {
			continue
		}
		entries, err := os.ReadDir(saveGames)
		if err != nil || len(entries) == 0 {
			continue
		}
		discovered = append(discovered, DiscoveredSave{
			ID:       "ue-" + sanitizeID(dir),
			Name:     dir + " (Epic/Unreal Save)",
			Type:     "game",
			SavePath: saveGames,
		})
	}

	// 6. User-configured custom scan paths (each subfolder = a candidate).
	for _, customPath := range customScanPaths {
		resolved, err := filepath.Abs(customPath)
		if err != nil || !dirExists(resolved) {
			continue
		}
		base := sanitizeID(filepath.Base(resolved))
		for _, sub := range listSubdirs(resolved) {
			discovered = append(discovered, DiscoveredSave{
				ID:       "custom-" + base + "-" + sanitizeID(sub),
				Name:     sub,
				Type:     "game",
				SavePath: filepath.Join(resolved, sub),
			})
		}
	}

	// Infer AppIDs from names for entries that lack one.
	nameIndex := nameToAppIDIndex()
	for i := range discovered {
		if discovered[i].AppID == "" {
			discovered[i].AppID = inferAppIDFromName(discovered[i].Name, nameIndex)
		}
	}

	sc.resolveNames(discovered)
	return discovered
}

// steamUserdataSystemIDs are userdata subfolders that are Steam client
// plumbing, not game saves.
var steamUserdataSystemIDs = map[string]bool{
	"7": true, "760": true, "241100": true, // client config, screenshots, controller configs
}

// libSystemDirs are library-root subfolders that are Steam's own, never a
// game install.
var libSystemDirs = map[string]bool{
	"steamapps": true, "steam": true, "userdata": true, "config": true,
	"appcache": true, "logs": true, "dumps": true, "bin": true,
	"package": true, "public": true, "clientui": true, "controller_base": true,
}

// scanSteamUserdata walks Steam's userdata/<user>/<appId> layout across
// all known install locations, naming entries from installed-game
// manifests when possible.
func (sc *Scanner) scanSteamUserdata(seen map[string]bool, appNames map[string]string) []DiscoveredSave {
	steamPaths := sc.SteamUserdataPaths
	if steamPaths == nil {
		for _, root := range sc.steamRootDirs() {
			steamPaths = append(steamPaths, filepath.Join(root, "userdata"))
		}
	}

	var found []DiscoveredSave
	for _, steamPath := range steamPaths {
		if !dirExists(steamPath) {
			continue
		}
		for _, user := range listSubdirs(steamPath) {
			userPath := filepath.Join(steamPath, user)
			for _, game := range listSubdirs(userPath) {
				// Game dirs are always numeric AppIDs; named siblings
				// (config, ugc, gamerecordings, …) are client plumbing.
				if !isAppID(game) || steamUserdataSystemIDs[game] {
					continue
				}
				gamePath := filepath.Join(userPath, game)
				normalized, err := filepath.Abs(gamePath)
				if err != nil {
					continue
				}
				if seen[normalized] {
					continue
				}
				seen[normalized] = true

				d := DiscoveredSave{
					ID:       fmt.Sprintf("steam-%s-%s", user, game),
					Name:     fmt.Sprintf("Steam User %s - AppID: %s", user, game),
					Type:     "game",
					SavePath: gamePath,
				}
				if isAppID(game) {
					d.AppID = game
				}
				if name := appNames[game]; name != "" {
					d.Name = name
				}
				found = append(found, d)
			}
		}
	}
	return found
}

// localLowVendorSkip filters LocalLow companies that are never games.
var localLowVendorSkip = map[string]bool{
	"microsoft": true, "adobe": true, "nvidia": true, "nvidia corporation": true,
	"intel": true, "intel corporation": true, "amd": true, "oracle": true,
	"sun": true, "temp": true, "google": true, "discord": true, "mozilla": true,
	"unity": true, "unitytechnologies": true, "unity technologies": true,
	"epic games": true, "valve": true, "acm": true,
}

// scanLocalLow discovers Unity's standard save convention:
// %USERPROFILE%/AppData/LocalLow/<Company>/<Game>.
func (sc *Scanner) scanLocalLow() []DiscoveredSave {
	root := sc.LocalLowDir
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "AppData", "LocalLow")
	}
	if !dirExists(root) {
		return nil
	}

	var found []DiscoveredSave
	for _, company := range listSubdirs(root) {
		if localLowVendorSkip[toLowerASCII(company)] || looksLikeHexHash(company) {
			continue
		}
		companyPath := filepath.Join(root, company)
		for _, game := range listSubdirs(companyPath) {
			gamePath := filepath.Join(companyPath, game)
			if !dirNonEmpty(gamePath) {
				continue
			}
			found = append(found, DiscoveredSave{
				ID:       "unity-" + sanitizeID(company) + "-" + sanitizeID(game),
				Name:     game,
				Type:     "game",
				SavePath: gamePath,
			})
		}
	}
	return found
}

// looksLikeHexHash reports cache dirs named as long hex digests (browser /
// shader caches that sometimes land in LocalLow).
func looksLikeHexHash(s string) bool {
	if len(s) < 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// resolveNames upgrades "Game ID: 12345"-style names to real titles:
// offline dictionary first, then the on-disk cache, then the injected
// resolver (Steam Store API) — concurrently, mirroring the JS
// Promise.allSettled batch.
func (sc *Scanner) resolveNames(discovered []DiscoveredSave) {
	cache := loadAppCache(sc.CacheFile)

	var mu sync.Mutex
	var wg sync.WaitGroup
	cacheDirty := false

	for i := range discovered {
		if discovered[i].AppID == "" {
			continue
		}
		appID := discovered[i].AppID

		if name, ok := popularSteamGames[appID]; ok {
			discovered[i].Name = name
			continue
		}

		mu.Lock()
		cachedName := cache[appID]
		mu.Unlock()
		if cachedName != "" {
			discovered[i].Name = cachedName
			continue
		}

		if sc.ResolveAppName == nil {
			continue
		}
		wg.Add(1)
		go func(i int, appID string) {
			defer wg.Done()
			name := sc.ResolveAppName(appID)
			if name == "" {
				return
			}
			mu.Lock()
			cache[appID] = name
			cacheDirty = true
			mu.Unlock()
			discovered[i].Name = name
		}(i, appID)
	}
	wg.Wait()

	if cacheDirty {
		saveAppCache(sc.CacheFile, cache)
	}
}

func dedupSet(items []DiscoveredSave) map[string]bool {
	seen := make(map[string]bool, len(items))
	for _, d := range items {
		if abs, err := filepath.Abs(d.SavePath); err == nil {
			seen[abs] = true
		}
	}
	return seen
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
