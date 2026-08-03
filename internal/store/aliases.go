package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrAmbiguousAppID is returned when several tracked games share an App ID,
// so there is no single correct match.
var ErrAmbiguousAppID = errors.New("multiple tracked games share this app id")

// FindGameByAppID returns the tracked game with the given (non-empty) Steam
// App ID. Used for cross-device matching when the user enables it, so a
// title tracked under different names on two PCs still resolves to one game.
//
// It deliberately refuses to guess when more than one local game carries the
// App ID — which is normal once a user tracks a game at several save
// locations. Picking one arbitrarily would let a peer's saves land in the
// wrong folder (and merge two distinct save sets), so ambiguity falls through
// to the explicit-link path instead.
func (s *Store) FindGameByAppID(appID string) (Game, error) {
	if appID == "" {
		return Game{}, ErrNotFound
	}
	var games []Game
	if err := s.db.Select(&games, `SELECT * FROM games WHERE app_id = ? ORDER BY name`, appID); err != nil {
		return Game{}, fmt.Errorf("find game by appid %s: %w", appID, err)
	}
	switch len(games) {
	case 0:
		return Game{}, ErrNotFound
	case 1:
		return games[0], nil
	default:
		return Game{}, ErrAmbiguousAppID
	}
}

// AddGameAlias records that aliasID refers to the same game as gameID on this
// device. A peer sync addressed to aliasID then resolves to gameID.
func (s *Store) AddGameAlias(aliasID, gameID string) error {
	if aliasID == "" || gameID == "" || aliasID == gameID {
		return fmt.Errorf("invalid game alias %q -> %q", aliasID, gameID)
	}
	_, err := s.db.Exec(
		`INSERT INTO game_aliases (alias_id, game_id, created_at_ms) VALUES (?, ?, ?)
		 ON CONFLICT(alias_id) DO UPDATE SET game_id = excluded.game_id`,
		aliasID, gameID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("add game alias %s: %w", aliasID, err)
	}
	return nil
}

// GameAlias is a link row plus the snapshot of the merged game kept for
// restore-on-unlink.
type GameAlias struct {
	AliasID  string `db:"alias_id"`
	GameID   string `db:"game_id"`
	Name     string `db:"alias_name"`
	SavePath string `db:"alias_save_path"`
	AppID    string `db:"alias_app_id"`
}

// GetGameAlias returns the full alias row (including the restore snapshot).
func (s *Store) GetGameAlias(aliasID string) (GameAlias, bool) {
	var a GameAlias
	err := s.db.Get(&a,
		`SELECT alias_id, game_id, alias_name, alias_save_path, alias_app_id
		 FROM game_aliases WHERE alias_id = ?`, aliasID)
	if err != nil {
		return GameAlias{}, false
	}
	return a, true
}

// SetAliasSnapshot records the merged game's identity on its alias row so a
// later unlink can restore it.
func (s *Store) SetAliasSnapshot(aliasID, name, savePath, appID string) error {
	_, err := s.db.Exec(
		`UPDATE game_aliases SET alias_name = ?, alias_save_path = ?, alias_app_id = ? WHERE alias_id = ?`,
		name, savePath, appID, aliasID)
	if err != nil {
		return fmt.Errorf("set alias snapshot %s: %w", aliasID, err)
	}
	return nil
}

// ResolveGameAlias maps a possibly-aliased id to the local canonical game id.
// The second return is false when aliasID isn't linked to anything.
func (s *Store) ResolveGameAlias(aliasID string) (string, bool) {
	var gameID string
	err := s.db.Get(&gameID, `SELECT game_id FROM game_aliases WHERE alias_id = ?`, aliasID)
	if err != nil {
		return "", false
	}
	return gameID, true
}

// CreateGameUnlessAliased inserts a game only if its id has not, in the
// meantime, been linked to one that already exists here. It returns the
// canonical id to use — the new game's, or the one the alias points at.
//
// Auto-tracking a peer's game is a check-then-create: look for a local game
// under that id, look for an alias, and create one when neither turns up.
// Linking is a separate write, made by the user or by App-ID matching, and
// nothing stopped it landing in between. Both sides then saw nothing to do —
// the link found no game to absorb because it had not been created yet, and
// the auto-track had already decided no link existed — and the peer's game
// appeared a second time under its own id, beside the entry it was just
// linked to.
//
// Doing both in one immediate transaction closes that: SQLite holds the write
// lock for the whole thing, so an alias committed by anyone else is either
// visible to the check or blocked until the insert is done.
func (s *Store) CreateGameUnlessAliased(g Game) (canonicalID string, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var aliasTarget string
	if err := tx.Get(&aliasTarget, `SELECT game_id FROM game_aliases WHERE alias_id = ?`, g.ID); err == nil && aliasTarget != "" {
		return aliasTarget, nil
	}
	// Someone may also have created the game itself in the window.
	var existing string
	if err := tx.Get(&existing, `SELECT id FROM games WHERE id = ?`, g.ID); err == nil && existing != "" {
		return existing, nil
	}

	if g.ActiveBranch == "" {
		g.ActiveBranch = "main"
	}
	if g.MaxSnapshots == 0 {
		g.MaxSnapshots = 20
	}
	if _, err := tx.NamedExec(`
		INSERT INTO games (id, name, save_path, active_branch, auto_sync, max_snapshots, max_manual_snapshots, app_id, exe_path, cover_url)
		VALUES (:id, :name, :save_path, :active_branch, :auto_sync, :max_snapshots, :max_manual_snapshots, :app_id, :exe_path, :cover_url)`,
		g); err != nil {
		return "", fmt.Errorf("insert game: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO branches (game_id, name) VALUES (?, ?)`, g.ID, g.ActiveBranch); err != nil {
		return "", fmt.Errorf("insert default branch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return g.ID, nil
}

// ListGameAliasDetails returns the alias rows pointing at a canonical game,
// including the merged game's remembered name and path — a bare id like
// "balatro-2" is meaningless to a user, especially when one title is tracked
// at several locations.
func (s *Store) ListGameAliasDetails(gameID string) ([]GameAlias, error) {
	var rows []GameAlias
	err := s.db.Select(&rows,
		`SELECT alias_id, game_id, alias_name, alias_save_path, alias_app_id
		 FROM game_aliases WHERE game_id = ? ORDER BY alias_id`, gameID)
	if err != nil {
		return nil, fmt.Errorf("list game alias details %s: %w", gameID, err)
	}
	return rows, nil
}

// ListGameAliases returns every alias id pointing at the given canonical game.
func (s *Store) ListGameAliases(gameID string) ([]string, error) {
	var ids []string
	if err := s.db.Select(&ids, `SELECT alias_id FROM game_aliases WHERE game_id = ? ORDER BY alias_id`, gameID); err != nil {
		return nil, fmt.Errorf("list game aliases %s: %w", gameID, err)
	}
	return ids, nil
}

// RemoveGameAlias drops a single link.
func (s *Store) RemoveGameAlias(aliasID string) error {
	if _, err := s.db.Exec(`DELETE FROM game_aliases WHERE alias_id = ?`, aliasID); err != nil {
		return fmt.Errorf("remove game alias %s: %w", aliasID, err)
	}
	return nil
}
