// Package daemon wires OpenSave's subsystems together: storage (with
// legacy import on first launch), the snapshot manager, the file watcher,
// and the local REST/WebSocket API. P2P and cloud attach here in later
// phases.
package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/cloud"
	"github.com/opensave/opensave/internal/config"
	"github.com/opensave/opensave/internal/logging"
	"github.com/opensave/opensave/internal/p2p"
	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/store/legacyimport"
	"github.com/opensave/opensave/internal/watcher"
)

// Options tune daemon construction; the zero value is production behavior.
type Options struct {
	// HomeOverride uses a custom data directory instead of ~/.opensave
	// (tests and portable installs).
	HomeOverride string
	// DisableDiscovery skips UDP LAN discovery (tests pair peers manually
	// and don't want broadcast traffic).
	DisableDiscovery bool
}

// Daemon is the assembled core. Access subsystems directly for operations
// (d.Store, d.Snapshots, ...).
type Daemon struct {
	Paths     config.Paths
	Store     *store.Store
	Snapshots *snapshot.Manager
	Watcher   *watcher.Engine
	Scanner   *presets.Scanner
	P2P       *p2p.Engine
	Cloud     *cloud.Service
	Log       *logging.Logger

	opts Options

	// OnGameChanged fires after a watcher-triggered auto-snapshot (in
	// addition to the built-in P2P sync kick). May be reassigned before
	// Start.
	OnGameChanged func(gameID string)
}

// New builds the daemon: resolves paths, runs the one-time legacy JSON
// import if needed, opens the store, and constructs (but does not start)
// the subsystems.
func New(opts Options) (*Daemon, error) {
	var paths config.Paths
	var err error
	if opts.HomeOverride != "" {
		paths, err = config.ResolveAt(opts.HomeOverride)
	} else {
		paths, err = config.Resolve()
	}
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}

	// Mirror the activity log to disk from the very first step so failures
	// that never reach the dashboard (boot problems, unreachable API) stay
	// diagnosable — the dashboard only exists once boot succeeds.
	log := logging.New()
	log.AttachFile(filepath.Join(paths.HomeDir, "opensave.log"))
	log.Log("info", "daemon starting (data dir: "+paths.HomeDir+")")
	fail := func(err error) (*Daemon, error) {
		log.Log("error", "daemon boot failed: "+err.Error())
		log.Close()
		return nil, err
	}

	if legacyimport.Needed(paths.LegacyDB, paths.SQLiteDB) {
		if err := legacyimport.Run(paths.LegacyDB, paths.SQLiteDB, paths.MigrationLog); err != nil {
			return fail(fmt.Errorf("legacy database import failed (your JSON data is untouched): %w", err))
		}
	}

	s, err := store.Open(paths.SQLiteDB)
	if err != nil {
		return fail(fmt.Errorf("open store: %w", err))
	}
	if err := s.EnsureDefaultSettings(paths.HomeDir, paths.BackupsDir); err != nil {
		s.Close()
		return fail(fmt.Errorf("initialize settings: %w", err))
	}

	snaps := snapshot.New(s)

	d := &Daemon{
		Paths:     paths,
		Store:     s,
		Snapshots: snaps,
		Log:       log,
		Scanner:   presets.NewScanner(paths.AppCacheFile),
		P2P:       p2p.New(s, snaps, log.Log),
		Cloud:     cloud.New(s, log.Log),
		opts:      opts,
	}

	// Every new snapshot mirrors to the configured cloud provider in the
	// background; failures are logged, never fatal.
	snaps.OnUpload = func(zipPath, remoteFileName string) {
		if err := d.Cloud.Upload(zipPath, remoteFileName); err != nil {
			if !cloud.IsNotConfigured(err) {
				log.Log("error", fmt.Sprintf("cloud upload of %s failed: %v", remoteFileName, err))
			}
			return
		}
		// Cloud-side retention mirrors the game's local snapshot limit:
		// keep the newest maxSnapshots per branch, delete the rest.
		gameID, branch, _, ok := snapshot.ParseExportEntryName(remoteFileName)
		if !ok {
			return
		}
		game, err := s.GetGame(gameID)
		if err != nil || game.MaxSnapshots <= 0 {
			return
		}
		prefix := fmt.Sprintf("%s__%s__", gameID, branch)
		_, _ = d.Cloud.PruneGameBranch(func(name string) bool {
			return strings.HasPrefix(name, prefix)
		}, game.MaxSnapshots)
	}

	d.Watcher = watcher.New(watcher.Callbacks{
		GetLastManifestHash: func(gameID string) (string, error) {
			game, err := s.GetGame(gameID)
			if err != nil {
				return "", err
			}
			return game.LastManifestHash, nil
		},
		SetLastManifestHash: s.SetLastManifestHash,
		CreateSnapshot: func(gameID string) error {
			_, err := snaps.Create(gameID, "", true)
			return err
		},
		OnChanged: func(gameID string) {
			// Watcher-detected save change: push it to online peers.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				if _, err := d.P2P.SyncGame(ctx, gameID); err != nil {
					d.Log.Log("info", fmt.Sprintf("post-snapshot sync for %s: %v", gameID, err))
				}
			}()
			if d.OnGameChanged != nil {
				d.OnGameChanged(gameID)
			}
		},
		Log: log.Log,
	})

	return d, nil
}

// Start begins watching every tracked game with auto-sync enabled.
func (d *Daemon) Start() error {
	games, err := d.Store.ListGames()
	if err != nil {
		return err
	}
	for _, game := range games {
		// Backfill cover art for games tracked before covers existed (or
		// migrated from the JS app without one).
		if game.CoverURL == "" {
			if cover := SteamCoverURL(game.AppID); cover != "" {
				game.CoverURL = cover
				_ = d.Store.UpdateGame(game)
			}
		}
		if !game.AutoSync {
			continue
		}
		if err := d.Watcher.Watch(game.ID, game.SavePath); err != nil {
			d.Log.Log("warn", fmt.Sprintf("could not watch %q: %v", game.Name, err))
		}
	}
	if !d.opts.DisableDiscovery {
		if err := d.P2P.StartDiscovery(); err != nil {
			d.Log.Log("warn", fmt.Sprintf("LAN discovery unavailable: %v", err))
		}
	}

	// Failsafe: automatically retry any game whose sync gets interrupted
	// (e.g. the network drops mid-transfer) once peers are reachable again.
	d.P2P.StartResyncLoop()

	// Join the WAN relay room if a sync code is configured (no-op without one).
	d.P2P.Wan.Connect()

	// Host an in-process relay if enabled.
	if settings, err := d.Store.GetSettings(); err == nil {
		d.P2P.ApplyRelayHosting(settings.HostRelay, settings.RelayPort)
	}

	d.Log.Log("info", fmt.Sprintf("daemon started; watching %d game(s)", len(games)))
	return nil
}

// Stop shuts the daemon down cleanly.
func (d *Daemon) Stop() {
	d.P2P.Stop()
	d.Watcher.Stop()
	d.Store.Close()
	d.Log.Close()
}

// SteamCoverURL returns Steam's CDN header art for an AppID ("" when the
// id isn't numeric). No API key needed; the CDN serves these publicly.
func SteamCoverURL(appID string) string {
	if appID == "" {
		return ""
	}
	for _, c := range appID {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return "https://cdn.cloudflare.steamstatic.com/steam/apps/" + appID + "/header.jpg"
}

// TrackGame adds a new game, takes its initial snapshot (when the save
// location already has content), and starts watching it.
func (d *Daemon) TrackGame(game store.Game) (store.Game, error) {
	if game.ID == "" {
		game.ID = store.SlugifyGameID(game.Name)
	}
	if game.ID == "" {
		return store.Game{}, fmt.Errorf("game name %q produces an empty id", game.Name)
	}
	game.AutoSync = true
	if game.ActiveBranch == "" {
		game.ActiveBranch = "main"
	}
	if game.MaxSnapshots == 0 {
		game.MaxSnapshots = 5
	}
	if game.CoverURL == "" {
		game.CoverURL = SteamCoverURL(game.AppID)
	}

	if err := d.Store.CreateGame(game); err != nil {
		return store.Game{}, err
	}

	if _, err := d.Snapshots.Create(game.ID, "Initial snapshot", true); err != nil {
		d.Log.Log("warn", fmt.Sprintf("initial snapshot for %q failed: %v", game.Name, err))
	}

	if err := d.Watcher.Watch(game.ID, game.SavePath); err != nil {
		d.Log.Log("warn", fmt.Sprintf("could not watch %q: %v", game.Name, err))
	}

	d.Log.Log("success", fmt.Sprintf("now tracking %q at %q", game.Name, game.SavePath))
	created, err := d.Store.GetGame(game.ID)
	if err != nil {
		return store.Game{}, err
	}
	return created, nil
}

// EnsureImportedSnapshot registers a snapshot restored from an .sscb
// backup: creates the branch if missing and inserts the metadata row
// unless that snapshot id is already known (idempotent re-imports).
func (d *Daemon) EnsureImportedSnapshot(gameID, branch, snapID, zipPath string, sizeBytes int64) error {
	if _, err := d.Store.GetSnapshot(snapID); err == nil {
		return nil // already registered
	}

	branches, err := d.Store.ListBranches(gameID)
	if err != nil {
		return err
	}
	haveBranch := false
	for _, b := range branches {
		if b == branch {
			haveBranch = true
			break
		}
	}
	if !haveBranch {
		if err := d.Store.CreateBranch(gameID, branch); err != nil {
			return err
		}
	}

	// snap_<ms> ids carry their creation time; reconstruct the timestamp
	// so imported snapshots sort correctly against existing ones.
	ts := snapIDToTimestamp(snapID)
	return d.Store.CreateSnapshot(store.Snapshot{
		ID:           snapID,
		GameID:       gameID,
		BranchName:   branch,
		Timestamp:    ts,
		Comment:      "Imported from backup",
		IsSystemAuto: true,
		ZipPath:      zipPath,
		SizeBytes:    sizeBytes,
	})
}

func snapIDToTimestamp(snapID string) string {
	msStr := strings.TrimPrefix(snapID, "snap_")
	ms, err := strconv.ParseInt(msStr, 10, 64)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// UntrackGame stops watching and removes a game. Snapshot zip files on
// disk are intentionally kept (they're the user's backups); only metadata
// is removed — same as the JS app.
func (d *Daemon) UntrackGame(gameID string) error {
	d.Watcher.Unwatch(gameID)
	return d.Store.DeleteGame(gameID)
}
