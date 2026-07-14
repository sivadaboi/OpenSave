package store

import "fmt"

// CreateBranch adds a new (initially empty) branch to a game.
func (s *Store) CreateBranch(gameID, name string) error {
	_, err := s.db.Exec(`INSERT INTO branches (game_id, name) VALUES (?, ?)`, gameID, name)
	if err != nil {
		return fmt.Errorf("create branch %s/%s: %w", gameID, name, err)
	}
	return nil
}

// ListBranches returns every branch name for a game.
func (s *Store) ListBranches(gameID string) ([]string, error) {
	var names []string
	if err := s.db.Select(&names, `SELECT name FROM branches WHERE game_id = ? ORDER BY name`, gameID); err != nil {
		return nil, fmt.Errorf("list branches for %s: %w", gameID, err)
	}
	return names, nil
}

// SwitchActiveBranch updates a game's active_branch pointer. Callers are
// responsible for the filesystem side (auto-snapshotting the outgoing
// branch, clearing the save folder, restoring the incoming branch's latest
// snapshot) via internal/snapshot before calling this — this method only
// updates the pointer once that has succeeded.
func (s *Store) SwitchActiveBranch(gameID, branchName string) error {
	res, err := s.db.Exec(`UPDATE games SET active_branch = ? WHERE id = ?`, branchName, gameID)
	if err != nil {
		return fmt.Errorf("switch active branch for %s: %w", gameID, err)
	}
	return checkRowAffected(res)
}
