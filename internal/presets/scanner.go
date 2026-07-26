package presets

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	// ManifestURL is where the Ludusavi manifest is fetched from. Empty
	// disables downloading (tests; any already-cached manifest still
	// applies when present on disk).
	ManifestURL string
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
	// GOOS selects the platform whose save conventions are scanned. Empty
	// means runtime.GOOS; tests set it to exercise Linux logic on any host.
	GOOS string
	// HomeDir overrides the user's home directory for Linux path resolution
	// (emulator paths, Proton prefixes). Empty means os.UserHomeDir. Tests
	// only.
	HomeDir string
}

// linuxHome returns the home dir used to resolve Linux save paths.
func (sc *Scanner) linuxHome() string {
	if sc.HomeDir != "" {
		return sc.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

// NewScanner builds a production Scanner using the Steam Store API
// resolver.
func NewScanner(cacheFile string) *Scanner {
	return &Scanner{
		CacheFile:      cacheFile,
		ResolveAppName: fetchSteamAppName,
		ManifestURL:    ludusaviManifestURL,
	}
}

// goos returns the platform to scan for (runtime default unless overridden).
func (sc *Scanner) goos() string {
	if sc.GOOS != "" {
		return sc.GOOS
	}
	return runtime.GOOS
}

const (
	// resolveConcurrency caps simultaneous Steam store-API name lookups.
	resolveConcurrency = 8
	// resolveFailCutoff is how many consecutive failed lookups mean the API
	// isn't reachable, so the rest of the scan skips it instead of paying a
	// timeout per game.
	resolveFailCutoff = 5
	// resolveBudget caps the total wall time a scan spends turning App IDs
	// into names. Steam's store API rate-limits, so a first scan on a machine
	// with a hundred unknown games would otherwise sit there for minutes —
	// the failure cutoff never fires because the lookups *succeed*, slowly.
	//
	// Whatever isn't resolved in time keeps its folder-derived placeholder;
	// every success is cached, so names fill in over the next couple of scans
	// instead of holding the first one hostage.
	resolveBudget = 6 * time.Second
)

// Scan sweeps every known save convention and returns the discovered
// locations with names resolved as well as possible (offline dictionary
// -> disk cache -> Steam Store API).
func (sc *Scanner) Scan(customScanPaths []string) []DiscoveredSave {
	var discovered []DiscoveredSave

	// 1 & 2. Emulator presets and repack wrapper folders (OS-appropriate
	// paths; a preset may resolve to several candidates on Linux).
	for _, p := range presetDefs {
		for _, resolved := range p.resolvedPaths(sc) {
			if !dirExists(resolved) {
				continue
			}
			if p.IsWrapper {
				for _, sub := range listSubdirs(resolved) {
					if wrapperSystemDirs[toLowerASCII(sub)] || isCacheDirName(sub) {
						continue
					}
					// Repack wrappers hold one AppID folder per game;
					// emulator wrappers (EmuDeck) hold one folder per
					// emulator — name them accordingly.
					name := fmt.Sprintf("%s - Game ID: %s", p.Name, sub)
					if p.Type == "emulator" {
						name = fmt.Sprintf("%s (%s)", p.Name, sub)
					}
					d := DiscoveredSave{
						ID:       p.ID + "-" + sub,
						Name:     name,
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

	// 3b-ii. Save folders found by name inside an install ("savegames",
	// "Saves", "SaveData"). Catches games that keep saves next to the game
	// under a layout no preset knows — Ubisoft titles using
	// <install>/savegames/<id>, for instance. Runs after the precise passes
	// so anything already pinpointed wins.
	for _, a := range apps {
		for _, p := range findSaveDirsUnder(a.InstallDir) {
			if seen[p] {
				continue
			}
			seen[p] = true
			discovered = append(discovered, DiscoveredSave{
				ID: "savedir-" + a.AppID + "-" + sanitizeID(filepath.Base(p)),
				Name: a.Name, Type: "game", SavePath: p, AppID: a.AppID,
			})
		}
	}
	for _, lib := range libraries {
		for _, sub := range listSubdirs(lib) {
			if libSystemDirs[toLowerASCII(sub)] {
				continue
			}
			for _, p := range findSaveDirsUnder(filepath.Join(lib, sub)) {
				if seen[p] {
					continue
				}
				seen[p] = true
				discovered = append(discovered, DiscoveredSave{
					ID:   "savedir-" + sanitizeID(sub) + "-" + sanitizeID(filepath.Base(p)),
					Name: sub, Type: "game", SavePath: p,
				})
			}
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
			if isCacheDirName(sub) {
				continue
			}
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
			if isCacheDirName(sub) {
				continue
			}
			discovered = append(discovered, DiscoveredSave{
				ID:       "custom-" + base + "-" + sanitizeID(sub),
				Name:     sub,
				Type:     "game",
				SavePath: filepath.Join(resolved, sub),
			})
		}
	}

	// 7. Ludusavi manifest: community-maintained save paths for tens of
	// thousands of games — store-agnostic, so repack/cracked installs
	// writing to the standard locations are found too. On Linux this also
	// expands Windows-style paths inside every Proton prefix.
	discovered = append(discovered, sc.scanLudusavi(dedupSet(discovered))...)

	// 8. Proton/Wine prefixes, coarse pass: list plausible save folders in
	// <library>/steamapps/compatdata/<appid>/pfx for games the manifest
	// did NOT pinpoint. Runs after Ludusavi on purpose — precise hits win,
	// and this pass skips anything containing one (a busy prefix's vendor
	// folders would otherwise flood the results; see the 38x "Persona 3
	// Reload" report).
	discovered = append(discovered, sc.scanProtonCompat(libraries, dedupSet(discovered), appNames)...)

	// 8b. Wine/Proton prefixes belonging to other launchers — Heroic, Lutris,
	// Bottles, a bare ~/.wine. This is where non-Steam and cracked games live
	// on Linux and on a Steam Deck; without it their saves were invisible no
	// matter how the game was installed.
	discovered = append(discovered, sc.scanWinePrefixes(dedupSet(discovered))...)

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

	// Bound the fan-out: one goroutine per uncached AppID meant a scan could
	// open hundreds of simultaneous connections to Steam's store API.
	sem := make(chan struct{}, resolveConcurrency)
	var consecutiveFails atomic.Int32
	var resolveGaveUp atomic.Bool
	deadline := time.Now().Add(resolveBudget)

	for i := range discovered {
		if discovered[i].AppID == "" {
			continue
		}
		appID := discovered[i].AppID

		if name, ok := popularSteamGames[appID]; ok {
			discovered[i].Name = renameKeepingSuffix(discovered[i].Name, name)
			continue
		}

		mu.Lock()
		cachedName := cache[appID]
		mu.Unlock()
		if cachedName != "" {
			discovered[i].Name = renameKeepingSuffix(discovered[i].Name, cachedName)
			continue
		}

		if sc.ResolveAppName == nil {
			continue
		}
		wg.Add(1)
		go func(i int, appID string) {
			defer wg.Done()

			// Give up on network lookups once it's clear they aren't working.
			// Each miss costs a full HTTP timeout, so on a network that blocks
			// Steam a big scan would otherwise pay that price hundreds of
			// times over and take tens of seconds. Unresolved names simply
			// keep their folder-derived placeholder, exactly as they do when
			// an individual lookup fails.
			if resolveGaveUp.Load() || time.Now().After(deadline) {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			// Re-check after queueing: the budget may have run out while this
			// goroutine waited for a slot.
			if resolveGaveUp.Load() || time.Now().After(deadline) {
				return
			}

			name := sc.ResolveAppName(appID)
			if name == "" {
				if consecutiveFails.Add(1) >= resolveFailCutoff {
					resolveGaveUp.Store(true)
				}
				return
			}
			consecutiveFails.Store(0)

			mu.Lock()
			cache[appID] = name
			cacheDirty = true
			mu.Unlock()
			discovered[i].Name = renameKeepingSuffix(discovered[i].Name, name)
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

// renameKeepingSuffix swaps a discovery's base title for the resolved one
// while keeping a trailing "(qualifier)". Proton-prefix entries are named
// "Title (SubDir)" — dropping the qualifier collapses a prefix's many
// candidates into identical, indistinguishable tiles.
func renameKeepingSuffix(oldName, newBase string) string {
	if i := strings.LastIndex(oldName, " ("); i >= 0 && strings.HasSuffix(oldName, ")") {
		suffix := oldName[i:]
		if !strings.HasSuffix(newBase, suffix) {
			return newBase + suffix
		}
	}
	return newBase
}

// seenInside reports whether an already-discovered save lives inside dir.
// Used by the coarse Proton listing: when the manifest already pinpointed
// a game's save folder within a prefix, offering the broad parent as well
// would only add noise.
func seenInside(seen map[string]bool, dir string) bool {
	prefix := toLowerASCII(dir) + string(filepath.Separator)
	for p := range seen {
		if strings.HasPrefix(toLowerASCII(p), prefix) {
			return true
		}
	}
	return false
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
