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
	MaxSnapshots int    `db:"max_snapshots" json:"maxSnapshots"`
	AppID        string `db:"app_id" json:"appId"`
	ExePath      string `db:"exe_path" json:"exePath"`
	CoverURL     string `db:"cover_url" json:"coverUrl"`
	// LastManifestHash is the manifest hash at the moment of the last
	// auto-snapshot; the watcher compares against it before snapshotting
	// again, preventing feedback loops (snapshot -> event -> snapshot).
	LastManifestHash string `db:"last_manifest_hash" json:"-"`
	CreatedAt        string `db:"created_at" json:"createdAt"`
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
		INSERT INTO games (id, name, save_path, active_branch, auto_sync, max_snapshots, app_id, exe_path, cover_url)
		VALUES (:id, :name, :save_path, :active_branch, :auto_sync, :max_snapshots, :app_id, :exe_path, :cover_url)`,
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
			app_id = :app_id,
			exe_path = :exe_path,
			cover_url = :cover_url,
			last_manifest_hash = :last_manifest_hash
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
