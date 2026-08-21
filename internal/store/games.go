package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	gameIDInvalidRe  = regexp.MustCompile(`[^a-z0-9]`)
	gameIDCollapseRe = regexp.MustCompile(`-+`)
)

// SlugifyGameID derives a clean game id from a display name, matching the
// JS app's rule: lowercase, non-alphanumerics to hyphens, collapsed and
// trimmed.
func SlugifyGameID(name string) string {
	id := gameIDInvalidRe.ReplaceAllString(strings.ToLower(name), "-")
	id = gameIDCollapseRe.ReplaceAllString(id, "-")
	return strings.Trim(id, "-")
}

// ErrNotFound is returned by single-row lookups when no matching row exists.
var ErrNotFound = errors.New("not found")

// Game is a tracked title/save-folder record.
type Game struct {
	ID           string `db:"id" json:"id"`
	Name         string `db:"name" json:"name"`
	SavePath     string `db:"save_path" json:"savePath"`
	ActiveBranch string `db:"active_branch" json:"activeBranch"`
	AutoSync     bool   `db:"auto_sync" json:"autoSync"`
	// MaxSnapshots caps AUTOMATIC snapshots — the ones the watcher takes as
	// the game saves. 0 or below keeps them all.
	MaxSnapshots int `db:"max_snapshots" json:"maxSnapshots"`
	// MaxManualSnapshots caps snapshots the user took deliberately. It is a
	// separate budget so a frequently auto-saving game cannot evict them;
	// 0 or below (the default) keeps them forever.
	MaxManualSnapshots int    `db:"max_manual_snapshots" json:"maxManualSnapshots"`
	AppID              string `db:"app_id" json:"appId"`
	ExePath            string `db:"exe_path" json:"exePath"`
	CoverURL           string `db:"cover_url" json:"coverUrl"`
	// LastManifestHash is the manifest hash at the moment of the last
	// auto-snapshot; the watcher compares against it before snapshotting
	// again, preventing feedback loops (snapshot -> event -> snapshot).
	//
	// It is written by the watcher after an automatic snapshot, and by
	// conflict resolution. Deliberately NOT by a snapshot taken any other way,
	// and NOT after a sync applies files — which looks like an oversight and
	// is not. Both were tried, measured, and reverted:
	//
	//   - Recording it on every snapshot fixed one failing test and broke
	//     another outright (6 runs, 6 failures). Nothing records after a pull,
	//     so the value goes stale the moment synced files land, and every
	//     later pull reads that staleness as uncaptured local work.
	//   - Recording it after a pull as well is worse than leaving it alone. A
	//     pull takes no safety snapshot of its own, so a file the user edited
	//     that was not part of that pull would be marked captured when it is
	//     not — and the next pull would overwrite it without asking. That
	//     trades a spurious conflict prompt for silent data loss.
	//
	// The sync engine no longer depends on it being fresh: it snapshots before
	// overwriting anything and reads this only as a fallback for when that
	// snapshot could not be taken (see filesAtRisk in the sync engine). Widen
	// who writes it and that fallback is what starts misfiring.
	LastManifestHash string `db:"last_manifest_hash" json:"-"`
	// SyncIgnore is the game's exclusion list, written like a .gitignore: one
	// pattern per line, "#" for comments. It decides what SYNCS and nothing
	// else — snapshots keep capturing every file, so a restore can never be
	// the thing that deletes an excluded config.
	SyncIgnore string `db:"sync_ignore" json:"syncIgnore"`
	CreatedAt  string `db:"created_at" json:"createdAt"`
}

// CreateGame inserts a new game and its default "main" branch in one
// transaction.
func (s *Store) CreateGame(g Game) error {
	if g.ActiveBranch == "" {
		g.ActiveBranch = "main"
	}
	if g.MaxSnapshots == 0 {
		g.MaxSnapshots = 20
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.NamedExec(`
		INSERT INTO games (id, name, save_path, active_branch, auto_sync, max_snapshots, max_manual_snapshots, app_id, exe_path, cover_url)
		VALUES (:id, :name, :save_path, :active_branch, :auto_sync, :max_snapshots, :max_manual_snapshots, :app_id, :exe_path, :cover_url)`,
		g)
	if err != nil {
		return fmt.Errorf("insert game: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO branches (game_id, name) VALUES (?, ?)`, g.ID, g.ActiveBranch); err != nil {
		return fmt.Errorf("insert default branch: %w", err)
	}
	return tx.Commit()
}

// GetGame returns a single game by ID.
func (s *Store) GetGame(id string) (Game, error) {
	var g Game
	err := s.db.Get(&g, `SELECT * FROM games WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	if err != nil {
		return Game{}, fmt.Errorf("get game %s: %w", id, err)
	}
	return g, nil
}

// FindGameBySavePath returns a tracked game whose save path matches (case-
// insensitively, matching the app's path handling elsewhere). Used to reject
// tracking the exact same folder twice.
func (s *Store) FindGameBySavePath(path string) (Game, error) {
	games, err := s.ListGames()
	if err != nil {
		return Game{}, err
	}
	target := strings.ToLower(strings.TrimRight(path, `\/`))
	for _, g := range games {
		if strings.ToLower(strings.TrimRight(g.SavePath, `\/`)) == target {
			return g, nil
		}
	}
	return Game{}, ErrNotFound
}

// ListGames returns every tracked game, ordered by name.
func (s *Store) ListGames() ([]Game, error) {
	var games []Game
	if err := s.db.Select(&games, `SELECT * FROM games ORDER BY name`); err != nil {
		return nil, fmt.Errorf("list games: %w", err)
	}
	return games, nil
}

// UpdateGame overwrites the mutable fields of a game (settings/patch-style
// update — id, save_path are updatable too since the UI allows relocating
// a tracked save folder).
func (s *Store) UpdateGame(g Game) error {
	res, err := s.db.NamedExec(`
		UPDATE games SET
			name = :name,
			save_path = :save_path,
			active_branch = :active_branch,
			auto_sync = :auto_sync,
			max_snapshots = :max_snapshots,
			max_manual_snapshots = :max_manual_snapshots,
			app_id = :app_id,
			exe_path = :exe_path,
			cover_url = :cover_url,
			last_manifest_hash = :last_manifest_hash,
			sync_ignore = :sync_ignore
		WHERE id = :id`, g)
	if err != nil {
		return fmt.Errorf("update game %s: %w", g.ID, err)
	}
	return checkRowAffected(res)
}

// SetLastManifestHash records the manifest hash captured at auto-snapshot
// time (see Game.LastManifestHash).
func (s *Store) SetLastManifestHash(gameID, hash string) error {
	res, err := s.db.Exec(`UPDATE games SET last_manifest_hash = ? WHERE id = ?`, hash, gameID)
	if err != nil {
		return fmt.Errorf("set last manifest hash %s: %w", gameID, err)
	}
	return checkRowAffected(res)
}

// DeleteGame removes a game and (via ON DELETE CASCADE) its branches,
// snapshots metadata, and sync-state rows. It does NOT delete the
// underlying snapshot ZIP files on disk — callers must do that themselves
// before/after calling DeleteGame using the zip_path values from
// ListSnapshots, matching the JS app's behavior of the caller owning
// filesystem cleanup.
func (s *Store) DeleteGame(id string) error {
	res, err := s.db.Exec(`DELETE FROM games WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete game %s: %w", id, err)
	}
	return checkRowAffected(res)
}

// AddUntrackedTombstone records that a game was deliberately untracked, so
// a peer that still tracks it can't silently auto-re-create it here.
func (s *Store) AddUntrackedTombstone(gameID string) error {
	_, err := s.db.Exec(
		`INSERT INTO untracked_games (game_id, untracked_at_ms) VALUES (?, ?)
		 ON CONFLICT(game_id) DO UPDATE SET untracked_at_ms = excluded.untracked_at_ms`,
		gameID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("add untracked tombstone %s: %w", gameID, err)
	}
	return nil
}

// IsUntracked reports whether a game id carries an untrack tombstone.
func (s *Store) IsUntracked(gameID string) bool {
	var one int
	err := s.db.Get(&one, `SELECT 1 FROM untracked_games WHERE game_id = ?`, gameID)
	return err == nil
}

// ClearUntrackedTombstone removes the tombstone (called when the user
// explicitly re-tracks the game).
func (s *Store) ClearUntrackedTombstone(gameID string) error {
	_, err := s.db.Exec(`DELETE FROM untracked_games WHERE game_id = ?`, gameID)
	return err
}

func checkRowAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
