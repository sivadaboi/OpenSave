package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FindGameByAppID returns the tracked game with the given (non-empty) Steam
// App ID. Used for cross-device matching when the user enables it, so a
// title tracked under different names on two PCs still resolves to one game.
func (s *Store) FindGameByAppID(appID string) (Game, error) {
	if appID == "" {
		return Game{}, ErrNotFound
	}
	var g Game
	err := s.db.Get(&g, `SELECT * FROM games WHERE app_id = ? ORDER BY name LIMIT 1`, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	if err != nil {
		return Game{}, fmt.Errorf("find game by appid %s: %w", appID, err)
	}
	return g, nil
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
