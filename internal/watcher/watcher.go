// Package watcher monitors tracked games' save locations and triggers
// auto-snapshots (and then sync) when they change, porting
// src/daemon/watcher.js with the resilience fixes from its author's
// walkthrough notes:
//
//   - Single-file saves are watched via their PARENT directory with events
//     filtered to the target file, so safe-write games (write temp file,
//     delete original, rename temp over it) don't break the watch.
//   - A changed save is only snapshotted once its files stop being locked
//     ("gameplay guard": poll every 5s while the game is still writing).
//   - Locked means an OS sharing violation only — read-only/permission
//     errors do not hang the guard loop.
//   - Before snapshotting, the current manifest hash is compared against
//     the hash recorded at the previous auto-snapshot, so restoring a
//     snapshot or receiving a sync doesn't feed back into another
//     snapshot of identical content.
package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/ignore"
)

const (
	debounceDelay      = 2 * time.Second
	guardPollInterval  = 5 * time.Second
	snapshotMaxRetries = 5
	snapshotRetryDelay = 1500 * time.Millisecond
)

// Callbacks connect the watcher to the rest of the daemon without import
// cycles. All are required.
type Callbacks struct {
	// IgnoreRules returns a game's exclusion list (the .gitignore-style text).
	// The recorded hash covers everything EXCEPT excluded paths, because the
	// sync engine compares against the same value to decide whether the save
	// holds changes a pull might overwrite — and a pull can never overwrite a
	// file it is not syncing. Counting excluded files here would make that
	// check fire on every pull, for a game whose config is simply newer than
	// its last snapshot.
	//
	// The cost is that editing ONLY an excluded file does not itself trigger
	// an automatic snapshot; it is still captured by the next snapshot taken
	// for any other reason. Optional: nil means nothing is excluded.
	IgnoreRules func(gameID string) string
	// GetLastManifestHash returns the hash recorded at the previous
	// auto-snapshot (empty string if none).
	GetLastManifestHash func(gameID string) (string, error)
	// SetLastManifestHash records the hash for the snapshot just taken.
	SetLastManifestHash func(gameID, hash string) error
	// CreateSnapshot takes the auto-snapshot. Retried on failure.
	CreateSnapshot func(gameID string) error
	// OnChanged fires after a successful auto-snapshot (the daemon uses it
	// to kick off P2P sync). Optional-in-behavior: may be nil.
	OnChanged func(gameID string)
	// Log receives human-readable watcher activity. May be nil.
	Log func(level, msg string)
}

// Engine owns one watch goroutine per tracked game.
type Engine struct {
	cb Callbacks

	mu     sync.Mutex
	games  map[string]*gameWatch
	closed bool
}

type gameWatch struct {
	gameID   string
	savePath string
	// extra maps a save location's name to its path on this device, for the
	// games whose save is split across more than one folder. Watched the same
	// way the main folder is: a change in any of them is a change to the game,
	// and without this a settings folder would only ever sync when the user
	// pressed the button.
	extra  map[string]string
	isFile bool
	fsw      *fsnotify.Watcher
	cancel   context.CancelFunc
	done     chan struct{}
}

// New creates a watcher Engine.
func New(cb Callbacks) *Engine {
	return &Engine{cb: cb, games: map[string]*gameWatch{}}
}

// Watch starts (or restarts) watching a game's save location.
//
// The expensive part — the recursive walk that registers every subfolder —
// happens BEFORE taking the engine lock: a big tree must never block
// Watch/Unwatch calls for other games (a wedged engine can otherwise only
// be fixed by restarting the app).
func (e *Engine) Watch(gameID, savePath string) error {
	return e.WatchWithLocations(gameID, savePath, nil)
}

// WatchWithLocations watches a game's main save folder plus any extra save
// locations it has, by name.
//
// One fsnotify watcher covers all of them: an event in any location means the
// game changed, and what follows — snapshot, then sync — is the same work
// whichever folder it came from.
func (e *Engine) WatchWithLocations(gameID, savePath string, extra map[string]string) error {
	isFile, err := delta.ResolveLocalSaveFilePath(savePath)
	if err != nil {
		return fmt.Errorf("inspect save path: %w", err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fs watcher: %w", err)
	}

	// Single-file saves: watch the parent directory (survives the file
	// being unlinked+recreated by safe-write); directory saves: watch the
	// tree recursively (fsnotify is non-recursive by itself).
	if isFile {
		if err := fsw.Add(filepath.Dir(savePath)); err != nil {
			fsw.Close()
			return fmt.Errorf("watch parent dir: %w", err)
		}
	} else {
		if err := os.MkdirAll(savePath, 0o777); err != nil {
			fsw.Close()
			return fmt.Errorf("create save dir: %w", err)
		}
		if err := addRecursive(fsw, savePath); err != nil {
			fsw.Close()
			return fmt.Errorf("watch save dir tree: %w", err)
		}
	}

	// A location that cannot be watched is logged and skipped rather than
	// failing the game: losing automatic snapshots of a mods folder is not a
	// reason to stop watching the save.
	watched := map[string]string{}
	for name, path := range extra {
		if path == "" {
			continue
		}
		if err := os.MkdirAll(path, 0o777); err != nil {
			e.log("warn", fmt.Sprintf("cannot watch the %q save location of %s: %v", name, gameID, err))
			continue
		}
		if err := addRecursive(fsw, path); err != nil {
			e.log("warn", fmt.Sprintf("cannot watch the %q save location of %s: %v", name, gameID, err))
			continue
		}
		watched[name] = path
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		fsw.Close()
		return fmt.Errorf("watcher engine is stopped")
	}
	if existing, ok := e.games[gameID]; ok {
		delete(e.games, gameID)
		e.mu.Unlock()
		existing.stop() // may wait on the old run goroutine — not under the lock
		e.mu.Lock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	gw := &gameWatch{
		gameID:   gameID,
		savePath: savePath,
		extra:    watched,
		isFile:   isFile,
		fsw:      fsw,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	e.games[gameID] = gw
	e.mu.Unlock()
	go e.run(ctx, gw)

	e.log("info", fmt.Sprintf("watching %q (%s mode)", savePath, map[bool]string{true: "single-file", false: "directory"}[isFile]))
	return nil
}

// Watching reports whether a game is currently being watched, and at which
// path. Reconciling the watch set against the database needs this: Watch
// replaces an existing watch outright — stopping its goroutine and re-adding
// every fsnotify registration — so re-watching everything to pick up one new
// game would tear down and rebuild all the others for nothing.
func (e *Engine) Watching(gameID string) (savePath string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	gw, ok := e.games[gameID]
	if !ok {
		return "", false
	}
	return gw.savePath, true
}

// WatchedGames returns the ids currently being watched.
func (e *Engine) WatchedGames() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.games))
	for id := range e.games {
		ids = append(ids, id)
	}
	return ids
}

// Unwatch stops watching a game. The (possibly slow) wait for the watch
// goroutine happens outside the engine lock so other games' operations
// are never blocked.
func (e *Engine) Unwatch(gameID string) {
	e.mu.Lock()
	gw, ok := e.games[gameID]
	if ok {
		delete(e.games, gameID)
	}
	e.mu.Unlock()
	if ok {
		gw.stop()
	}
}

// Stop shuts down every watch goroutine.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.closed = true
	stopping := make([]*gameWatch, 0, len(e.games))
	for id, gw := range e.games {
		stopping = append(stopping, gw)
		delete(e.games, id)
	}
	e.mu.Unlock()
	for _, gw := range stopping {
		gw.stop()
	}
}

func (gw *gameWatch) stop() {
	gw.cancel()
	gw.fsw.Close()
	<-gw.done
}

// run is the per-game event loop: filter -> debounce -> guard -> snapshot.
func (e *Engine) run(ctx context.Context, gw *gameWatch) {
	defer close(gw.done)

	var debounce *time.Timer
	var debounceC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-gw.fsw.Events:
			if !ok {
				return
			}
			if !gw.eventRelevant(event) {
				continue
			}
			// New subdirectory in directory mode: extend the watch.
			if !gw.isFile && event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addRecursive(gw.fsw, event.Name)
				}
			}
			if debounce == nil {
				debounce = time.NewTimer(debounceDelay)
				debounceC = debounce.C
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(debounceDelay)
			}

		case _, ok := <-gw.fsw.Errors:
			if !ok {
				return
			}

		case <-debounceC:
			debounce = nil
			debounceC = nil
			e.handleChange(ctx, gw)
		}
	}
}

// eventRelevant filters raw fs events: dotfiles are ignored everywhere; in
// single-file mode only events on the save file itself count (compared
// case-insensitively on Windows via EqualFold — save paths there are
// case-preserving but insensitive).
func (gw *gameWatch) eventRelevant(event fsnotify.Event) bool {
	name := filepath.Base(event.Name)
	if strings.HasPrefix(name, ".") {
		return false
	}
	if strings.HasSuffix(name, ".opensave.tmp") {
		return false
	}
	if gw.isFile {
		return strings.EqualFold(
			filepath.Clean(event.Name),
			filepath.Clean(gw.savePath),
		)
	}
	return true
}

// handleChange runs after the debounce window: wait out any file locks
// (gameplay guard), skip if content is unchanged since the last
// auto-snapshot, then snapshot with retries and notify.
func (e *Engine) handleChange(ctx context.Context, gw *gameWatch) {
	// Gameplay guard: the game may still be mid-write.
	for anyFileLocked(gw.savePath) {
		e.log("info", fmt.Sprintf("save files for %q are in use; waiting (gameplay guard)", gw.gameID))
		select {
		case <-ctx.Done():
			return
		case <-time.After(guardPollInterval):
		}
	}

	manifest, failures, err := delta.BuildMultiManifest(gw.savePath, gw.extra)
	if err != nil {
		e.log("warn", fmt.Sprintf("manifest build failed for %q: %v", gw.gameID, err))
		return
	}
	for name, failure := range failures {
		e.log("warn", fmt.Sprintf("cannot read the %q save location of %s: %v", name, gw.gameID, failure))
	}
	// ContentHash, not ManifestHash: this asks "did anything about this game
	// change", which has to cover every one of its folders. For a game with
	// one folder the two are the same value, so nothing already recorded is
	// invalidated by the upgrade.
	if e.cb.IgnoreRules != nil {
		if rules := ignore.Parse(e.cb.IgnoreRules(gw.gameID)); !rules.Empty() {
			manifest = filterForHash(manifest, rules)
		}
	}
	currentHash := manifest.ContentHash()

	lastHash, err := e.cb.GetLastManifestHash(gw.gameID)
	if err == nil && lastHash == currentHash {
		e.log("info", fmt.Sprintf("no content change for %q; skipping auto-snapshot", gw.gameID))
		return
	}

	for attempt := 1; attempt <= snapshotMaxRetries; attempt++ {
		err = e.cb.CreateSnapshot(gw.gameID)
		if err == nil {
			break
		}
		e.log("warn", fmt.Sprintf("auto-snapshot failed for %q (attempt %d/%d): %v", gw.gameID, attempt, snapshotMaxRetries, err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(snapshotRetryDelay):
		}
	}
	if err != nil {
		e.log("error", fmt.Sprintf("auto-snapshot permanently failed for %q: %v", gw.gameID, err))
		return
	}

	if err := e.cb.SetLastManifestHash(gw.gameID, currentHash); err != nil {
		e.log("warn", fmt.Sprintf("failed to record manifest hash for %q: %v", gw.gameID, err))
	}
	e.log("success", fmt.Sprintf("auto-snapshot created for %q", gw.gameID))

	if e.cb.OnChanged != nil {
		e.cb.OnChanged(gw.gameID)
	}
}

// anyFileLocked walks the save location and reports whether any file in it
// is currently held with an incompatible sharing mode.
func anyFileLocked(savePath string) bool {
	info, err := os.Stat(savePath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return isFileLocked(savePath)
	}
	locked := false
	filepath.Walk(savePath, func(path string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil || walkInfo.IsDir() {
			return nil
		}
		if isFileLocked(path) {
			locked = true
			return filepath.SkipAll
		}
		return nil
	})
	return locked
}

// addRecursive registers root and every subdirectory with the fs watcher.
func addRecursive(fsw *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable subdir: skip, don't fail the whole watch
		}
		if info.IsDir() {
			if strings.HasPrefix(filepath.Base(path), ".") && path != root {
				return filepath.SkipDir
			}
			return fsw.Add(path)
		}
		return nil
	})
}

func (e *Engine) log(level, msg string) {
	if e.cb.Log != nil {
		e.cb.Log(level, msg)
	}
}

// filterForHash drops excluded paths before the content hash is taken, so the
// value recorded here means the same thing the sync engine means by it.
func filterForHash(m delta.Manifest, rules ignore.Rules) delta.Manifest {
	out := delta.Manifest{Files: make(map[string]delta.FileEntry, len(m.Files))}
	for p, entry := range m.Files {
		if !rules.Match(p) {
			out.Files[p] = entry
		}
	}
	for _, d := range m.Dirs {
		if !rules.Match(d) {
			out.Dirs = append(out.Dirs, d)
		}
	}
	if len(m.Extra) > 0 {
		out.Extra = make(map[string]delta.RootManifest, len(m.Extra))
		for name, root := range m.Extra {
			sub := delta.RootManifest{Files: make(map[string]delta.FileEntry, len(root.Files))}
			for p, entry := range root.Files {
				if !rules.Match(p) {
					sub.Files[p] = entry
				}
			}
			for _, d := range root.Dirs {
				if !rules.Match(d) {
					sub.Dirs = append(sub.Dirs, d)
				}
			}
			out.Extra[name] = sub
		}
	}
	return out
}
