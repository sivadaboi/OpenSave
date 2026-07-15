package presets

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// Ludusavi manifest integration.
//
// The Ludusavi manifest (github.com/mtkennerly/ludusavi-manifest) is a
// community-maintained database of save locations for tens of thousands of
// games, sourced from PCGamingWiki. Detection is purely by path pattern —
// no store validation — so it finds saves for Steam, GOG, Epic, itch, and
// cracked/repack installs alike, as long as the game writes to its usual
// location.
//
// The raw manifest is a large YAML file. We download it at most once per
// week, distill the Windows-relevant entries into a compact JSON index
// next to it, and expand + stat the patterns during a scan.

const ludusaviManifestURL = "https://raw.githubusercontent.com/mtkennerly/ludusavi-manifest/master/data/manifest.yaml"

const manifestMaxAge = 7 * 24 * time.Hour

// manifestFileEntry / manifestGame model just the YAML we care about.
type manifestFileEntry struct {
	Tags []string `yaml:"tags"`
	When []struct {
		OS    string `yaml:"os"`
		Store string `yaml:"store"`
	} `yaml:"when"`
}

type manifestGame struct {
	Files      map[string]manifestFileEntry `yaml:"files"`
	InstallDir map[string]struct{}          `yaml:"installDir"`
	Steam      struct {
		ID int64 `yaml:"id"`
	} `yaml:"steam"`
}

// indexedGame is the compact, pre-filtered form persisted as JSON so scans
// don't re-parse megabytes of YAML.
type indexedGame struct {
	Name     string   `json:"n"`
	SteamID  string   `json:"s,omitempty"`
	Installs []string `json:"i,omitempty"` // installDir folder names
	Paths    []string `json:"p"`           // Windows-relevant save path templates
}

// manifestPaths derives the manifest + index locations from the scanner's
// cache file directory (~/.opensave).
func (sc *Scanner) manifestPaths() (yamlPath, indexPath string) {
	dir := filepath.Dir(sc.CacheFile)
	return filepath.Join(dir, "ludusavi-manifest.yaml"), filepath.Join(dir, "ludusavi-index.json")
}

// scanLudusavi expands the manifest's save-path templates and returns the
// locations that actually exist on this machine.
func (sc *Scanner) scanLudusavi(seen map[string]bool) []DiscoveredSave {
	if sc.CacheFile == "" {
		return nil
	}
	games := sc.loadManifestIndex()
	if len(games) == 0 {
		return nil
	}

	vars := windowsPathVars()
	if vars == nil {
		return nil
	}
	blocked := blockedRoots(vars)
	baseDirs := sc.installBaseCandidates()

	var mu sync.Mutex
	var found []DiscoveredSave
	markSeen := func(p string) bool { // true if fresh
		mu.Lock()
		defer mu.Unlock()
		if seen[p] {
			return false
		}
		seen[p] = true
		return true
	}

	jobs := make(chan indexedGame)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				dirs := expandGamePaths(g, vars, baseDirs, blocked)
				for i, dir := range dirs {
					abs, err := filepath.Abs(dir)
					if err != nil || !markSeen(abs) {
						continue
					}
					id := "ludusavi-" + sanitizeID(g.Name)
					name := g.Name
					if len(dirs) > 1 {
						id += "-" + strconv.Itoa(i)
						name += " (" + filepath.Base(dir) + ")"
					}
					mu.Lock()
					found = append(found, DiscoveredSave{
						ID: id, Name: name, Type: "game", SavePath: abs, AppID: g.SteamID,
					})
					mu.Unlock()
				}
			}
		}()
	}
	for _, g := range games {
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	return found
}

// expandGamePaths resolves one game's templates to existing directories.
func expandGamePaths(g indexedGame, vars map[string]string, baseDirs func([]string) []string, blocked map[string]bool) []string {
	installBases := baseDirs(g.Installs)

	var hits []string
	push := func(dir string) {
		if dir == "" || blocked[strings.ToLower(dir)] {
			return
		}
		for _, d := range hits {
			if d == dir {
				return
			}
		}
		if len(hits) < 12 {
			hits = append(hits, dir)
		}
	}

	for _, tpl := range g.Paths {
		for _, expanded := range expandTemplate(tpl, vars, installBases, g.SteamID) {
			for _, hit := range statOrGlob(expanded) {
				push(hit)
			}
		}
	}

	// Collapse nesting: when one hit contains another (game dir plus its
	// profile subdir plus Screenshots…), tracking the ancestor covers all
	// of it — keep only the outermost dirs.
	var out []string
	for _, cand := range hits {
		nested := false
		for _, other := range hits {
			if other != cand && isSubPath(other, cand) {
				nested = true
				break
			}
		}
		if !nested && len(out) < 3 {
			out = append(out, cand)
		}
	}
	return out
}

// isSubPath reports whether child lives inside parent.
func isSubPath(parent, child string) bool {
	p := strings.ToLower(parent) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(child), p)
}

// expandTemplate substitutes <placeholders>; a template may fan out to
// several concrete patterns (one per install-dir candidate). Templates
// with leftover placeholders are dropped.
func expandTemplate(tpl string, vars map[string]string, installBases []string, steamID string) []string {
	s := tpl
	for k, v := range vars {
		s = strings.ReplaceAll(s, k, v)
	}
	if steamID != "" {
		s = strings.ReplaceAll(s, "<storeGameId>", steamID)
	} else {
		s = strings.ReplaceAll(s, "<storeGameId>", "*")
	}
	s = strings.ReplaceAll(s, "<storeUserId>", "*")

	var cands []string
	if strings.Contains(s, "<base>") || strings.Contains(s, "<game>") || strings.Contains(s, "<root>") {
		for _, base := range installBases {
			c := strings.ReplaceAll(s, "<base>", base)
			c = strings.ReplaceAll(c, "<game>", filepath.Base(base))
			c = strings.ReplaceAll(c, "<root>", filepath.Dir(base))
			cands = append(cands, c)
		}
	} else {
		cands = []string{s}
	}

	var out []string
	for _, c := range cands {
		if strings.ContainsRune(c, '<') { // unresolved placeholder
			continue
		}
		p := filepath.FromSlash(c)
		if !filepath.IsAbs(p) {
			// A relative template would resolve against the process CWD —
			// never a save location.
			continue
		}
		out = append(out, p)
	}
	return out
}

// statOrGlob resolves one concrete pattern to existing directories: globs
// wildcards, maps files to their parent dir, and falls back one level when
// the exact leaf doesn't exist (pattern names a file that isn't there yet,
// but its folder is).
func statOrGlob(pattern string) []string {
	toDir := func(p string) string {
		info, err := os.Stat(p)
		if err != nil {
			return ""
		}
		if info.IsDir() {
			return p
		}
		return filepath.Dir(p)
	}

	if strings.ContainsAny(pattern, "*?[") {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			return nil
		}
		var dirs []string
		for _, m := range matches {
			if d := toDir(m); d != "" {
				dirs = append(dirs, d)
			}
			if len(dirs) >= 4 {
				break
			}
		}
		return dirs
	}

	if d := toDir(pattern); d != "" {
		return []string{d}
	}
	// Leaf missing: no parent-dir fallback. It sounds helpful, but in
	// practice it surfaces shared engine roots (RenPy, Godot, a studio's
	// LocalLow folder) as "the save" — and a game with no save files yet
	// has nothing worth tracking anyway.
	return nil
}

// windowsPathVars maps manifest placeholders to this machine's paths.
func windowsPathVars() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	username := os.Getenv("USERNAME")
	if username == "" {
		username = filepath.Base(home)
	}
	return map[string]string{
		"<home>":               home,
		"<winAppData>":         ResolvePath("%APPDATA%"),
		"<winLocalAppData>":    ResolvePath("%LOCALAPPDATA%"),
		"<winLocalAppDataLow>": filepath.Join(home, "AppData", "LocalLow"),
		"<winDocuments>":       filepath.Join(home, "Documents"),
		"<winPublic>":          ResolvePath("%PUBLIC%"),
		"<winProgramData>":     ResolvePath("%PROGRAMDATA%"),
		"<osUserName>":         username,
	}
}

// blockedRoots are directories too broad to ever offer as a save location
// (a loose manifest pattern must never suggest tracking all of Documents).
func blockedRoots(vars map[string]string) map[string]bool {
	blocked := map[string]bool{}
	add := func(p string) {
		if p != "" {
			blocked[strings.ToLower(p)] = true
		}
	}
	for _, v := range vars {
		add(v)
	}
	home := vars["<home>"]
	add(filepath.Join(home, "Documents", "My Games"))
	add(filepath.Join(home, "Saved Games"))
	add(filepath.Join(home, "Desktop"))
	add(filepath.Join(home, "Downloads"))
	add(filepath.Join(vars["<winPublic>"], "Documents"))
	add(filepath.Join(vars["<winLocalAppData>"], "Programs"))
	add(filepath.Join(vars["<winLocalAppData>"], "Packages"))
	add(filepath.Join(vars["<winLocalAppData>"], "User Data"))
	// Shared engine roots hold every game made with that engine — a match
	// must point at a specific game's folder inside them, never the root.
	add(filepath.Join(vars["<winAppData>"], "RenPy"))
	add(filepath.Join(vars["<winAppData>"], "Godot"))
	add(filepath.Join(vars["<winAppData>"], "Godot", "app_userdata"))
	add(filepath.Join(vars["<winAppData>"], "LOVE"))
	add(filepath.Join(vars["<winLocalAppDataLow>"], "DefaultCompany"))
	return blocked
}

// installBaseCandidates returns a resolver from installDir names to
// existing absolute install paths: Steam's steamapps/common across every
// library, plus game folders sitting next to steamapps (where portable
// and cracked installs usually live).
func (sc *Scanner) installBaseCandidates() func([]string) []string {
	libs := sc.steamLibraryPaths()
	installed := map[string]string{}
	for _, a := range steamInstalledApps(libs) {
		installed[strings.ToLower(filepath.Base(a.InstallDir))] = a.InstallDir
	}
	return func(names []string) []string {
		var out []string
		for _, name := range names {
			if p := installed[strings.ToLower(name)]; p != "" {
				out = append(out, p)
				continue
			}
			for _, lib := range libs {
				for _, cand := range []string{
					filepath.Join(lib, "steamapps", "common", name),
					filepath.Join(lib, name),
				} {
					if dirExists(cand) {
						out = append(out, cand)
						break
					}
				}
			}
		}
		return out
	}
}

// ── embedded snapshot + manifest download + index ───────────────────────

// A compressed snapshot of the index ships inside the binary, so the very
// first scan works instantly and fully offline — no download required.
// Fresher data still arrives via the background weekly refresh; a local
// downloaded manifest always outranks the embedded snapshot.
// Regenerate with: GEN_EMBED=1 go test ./internal/presets/ -run GenerateEmbeddedIndex
//
//go:embed embedded/ludusavi-index.json.gz
var embeddedFS embed.FS

var embeddedIndex struct {
	once  sync.Once
	games []indexedGame
}

func loadEmbeddedIndex() []indexedGame {
	embeddedIndex.once.Do(func() {
		f, err := embeddedFS.Open("embedded/ludusavi-index.json.gz")
		if err != nil {
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return
		}
		defer zr.Close()
		raw, err := io.ReadAll(zr)
		if err != nil {
			return
		}
		_ = json.Unmarshal(raw, &embeddedIndex.games)
	})
	return embeddedIndex.games
}

// loadManifestIndex returns the compact index: a locally downloaded
// manifest when present, the embedded snapshot otherwise. A refresh runs
// in the background when the local copy is missing or stale — scans never
// wait on the network. The embedded fallback only applies when the
// manifest feature is enabled (ManifestURL set — always true in
// production; hermetic tests leave it empty).
func (sc *Scanner) loadManifestIndex() []indexedGame {
	yamlPath, indexPath := sc.manifestPaths()

	embedded := func() []indexedGame {
		if sc.ManifestURL == "" {
			return nil
		}
		return loadEmbeddedIndex()
	}

	sc.refreshManifestAsync(yamlPath)

	yamlInfo, err := os.Stat(yamlPath)
	if err != nil {
		return embedded()
	}

	// Reuse the index when it's newer than the YAML it was built from.
	if idxInfo, err := os.Stat(indexPath); err == nil && idxInfo.ModTime().After(yamlInfo.ModTime()) {
		if raw, err := os.ReadFile(indexPath); err == nil {
			var games []indexedGame
			if json.Unmarshal(raw, &games) == nil && len(games) > 0 {
				return games
			}
		}
	}

	games := buildManifestIndex(yamlPath)
	if len(games) > 0 {
		if raw, err := json.Marshal(games); err == nil {
			_ = os.WriteFile(indexPath, raw, 0o666)
		}
		return games
	}
	return embedded()
}

// refreshInFlight guards against overlapping background downloads.
var refreshInFlight atomic.Bool

// refreshManifestAsync kicks off a background download when the local
// manifest is missing or older than a week. The current scan proceeds
// with whatever is available now; the next scan sees the fresh data.
func (sc *Scanner) refreshManifestAsync(yamlPath string) {
	url := sc.ManifestURL
	if url == "" {
		return
	}
	if info, err := os.Stat(yamlPath); err == nil && time.Since(info.ModTime()) < manifestMaxAge {
		return
	}
	if !refreshInFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer refreshInFlight.Store(false)
		sc.downloadManifest(yamlPath)
	}()
}

// downloadManifest fetches the manifest. Best-effort: failures leave any
// existing copy in place.
func (sc *Scanner) downloadManifest(yamlPath string) {
	url := sc.ManifestURL
	if url == "" {
		return
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	tmp := yamlPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmp)
		return
	}
	_ = os.Remove(yamlPath)
	_ = os.Rename(tmp, yamlPath)
}

// buildManifestIndex parses the manifest YAML and keeps only what a
// Windows save scan needs.
func buildManifestIndex(yamlPath string) []indexedGame {
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil
	}
	var manifest map[string]manifestGame
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return nil
	}

	games := make([]indexedGame, 0, len(manifest))
	for name, mg := range manifest {
		var paths []string
		for tpl, entry := range mg.Files {
			if !entryIsWindowsSave(tpl, entry) {
				continue
			}
			paths = append(paths, tpl)
		}
		if len(paths) == 0 {
			continue
		}
		g := indexedGame{Name: name, Paths: paths}
		if mg.Steam.ID > 0 {
			g.SteamID = strconv.FormatInt(mg.Steam.ID, 10)
		}
		for dir := range mg.InstallDir {
			g.Installs = append(g.Installs, dir)
		}
		games = append(games, g)
	}
	return games
}

// entryIsWindowsSave filters manifest file entries down to Windows save
// data (untagged entries count as saves, config-only entries don't).
func entryIsWindowsSave(tpl string, entry manifestFileEntry) bool {
	// Linux/Mac-only path bases can never resolve on Windows.
	if strings.Contains(tpl, "<xdgConfig>") || strings.Contains(tpl, "<xdgData>") ||
		strings.Contains(tpl, "<winDir>") || strings.Contains(tpl, "<dataDrive>") {
		return false
	}

	if len(entry.Tags) > 0 {
		hasSave := false
		for _, t := range entry.Tags {
			if t == "save" {
				hasSave = true
				break
			}
		}
		if !hasSave {
			return false
		}
	}

	if len(entry.When) == 0 {
		return true
	}
	for _, w := range entry.When {
		if w.OS == "" || w.OS == "windows" {
			return true
		}
	}
	return false
}
