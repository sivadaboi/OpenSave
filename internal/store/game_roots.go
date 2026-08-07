package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GameRoot is one additional save location for a game, beyond the primary
// Game.SavePath.
//
// Name is what travels between devices; Path is local to this one. The two
// are deliberately separate: "config" means the same thing on a PC and on a
// Steam Deck, while the path it resolves to does not.
type GameRoot struct {
	GameID  string `db:"game_id" json:"-"`
	Name    string `db:"name" json:"name"`
	Path    string `db:"path" json:"path"`
	Ordinal int    `db:"ordinal" json:"-"`
	// CreatedAt is scanned but not surfaced; the UI orders by Ordinal.
	CreatedAt string `db:"created_at" json:"-"`
}

// Mapped reports whether this device knows where the root actually lives. A
// root learned from a peer starts unmapped, and syncing must skip it rather
// than guess — see NormalizeRootName for why the name alone is not enough to
// locate anything.
func (r GameRoot) Mapped() bool { return strings.TrimSpace(r.Path) != "" }

// NormalizeRootName reduces a user-supplied label to the identifier both
// devices will agree on. Names are matched across machines, so "Config",
// "config " and "config" have to be the same root or the two sides would
// each sync half the game into a folder the other never looks at.
//
// The empty string is reserved for the primary location (Game.SavePath) and
// is never a valid extra-root name.
func NormalizeRootName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "\\", "-")
	n = strings.ReplaceAll(n, "/", "-")
	return strings.Trim(n, "-")
}

// ListGameRoots returns a game's extra save locations, primary excluded.
func (s *Store) ListGameRoots(gameID string) ([]GameRoot, error) {
	var roots []GameRoot
	err := s.db.Select(&roots,
		`SELECT * FROM game_roots WHERE game_id = ? ORDER BY ordinal, name`, gameID)
	if err != nil {
		return nil, fmt.Errorf("list game roots: %w", err)
	}
	return roots, nil
}

// overlaps reports whether two save locations describe the same files —
// identical paths, or one inside the other.
//
// Locations that overlap are not merely redundant, they actively fight: the
// same file appears in two manifests under two names, so each sync patches
// it twice, and a deletion propagated for one location is seen by the other
// as a file the peer is missing and pushed straight back. Refusing the
// overlap is the only way to keep each file answerable to exactly one
// location.
func overlaps(a, b string) bool {
	pa, err1 := filepath.Abs(strings.TrimSpace(a))
	pb, err2 := filepath.Abs(strings.TrimSpace(b))
	if err1 != nil || err2 != nil {
		return false
	}
	pa, pb = filepath.Clean(pa), filepath.Clean(pb)
	if strings.EqualFold(pa, pb) {
		return true
	}
	sep := string(os.PathSeparator)
	// Case-insensitive: on Windows C:\Saves and c:\saves\config are the same
	// tree, and a comparison that missed that would let the overlap through.
	la, lb := strings.ToLower(pa)+sep, strings.ToLower(pb)+sep
	return strings.HasPrefix(lb, la) || strings.HasPrefix(la, lb)
}

// AddGameRoot records an extra location, or updates the path of one already
// known by that name. Learning a root from a peer and later being told where
// it lives here is the same operation.
//
// A path that overlaps the game's primary location or another of its
// locations is refused; see overlaps.
func (s *Store) AddGameRoot(gameID, name, path string) error {
	n := NormalizeRootName(name)
	if n == "" {
		return fmt.Errorf("a save location needs a name")
	}

	if strings.TrimSpace(path) != "" {
		game, err := s.GetGame(gameID)
		if err != nil {
			return fmt.Errorf("add game root: %w", err)
		}
		if overlaps(game.SavePath, path) {
			return fmt.Errorf("%q is the game's main save folder, or inside it — every file there is already being synced", path)
		}
		existing, err := s.ListGameRoots(gameID)
		if err != nil {
			return err
		}
		for _, r := range existing {
			if r.Name == n || !r.Mapped() {
				continue
			}
			if overlaps(r.Path, path) {
				return fmt.Errorf("%q overlaps the %q location — one file cannot belong to two locations", path, r.Name)
			}
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO game_roots (game_id, name, path, ordinal)
		VALUES (?, ?, ?, COALESCE((SELECT MAX(ordinal) + 1 FROM game_roots WHERE game_id = ?), 0))
		ON CONFLICT(game_id, name) DO UPDATE SET path = excluded.path`,
		gameID, n, path, gameID)
	if err != nil {
		return fmt.Errorf("add game root: %w", err)
	}
	return nil
}

// RemoveGameRoot forgets an extra location. The files it pointed at are left
// alone — this stops tracking a folder, it does not delete a save.
//
// Its lineage with every peer goes too: a location re-added under the same
// name later must start from no agreed state, not inherit a merge base
// describing files that may be long gone.
func (s *Store) RemoveGameRoot(gameID, name string) error {
	n := NormalizeRootName(name)
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM game_roots WHERE game_id = ? AND name = ?`, gameID, n); err != nil {
		return fmt.Errorf("remove game root: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM game_root_sync_state WHERE game_id = ? AND root = ?`, gameID, n); err != nil {
		return fmt.Errorf("remove game root lineage: %w", err)
	}
	return tx.Commit()
}

// GameRootPaths maps root name to this device's path, for the roots that are
// actually mapped here. Unmapped roots are omitted: a caller iterating this
// map can never accidentally sync into an empty path.
func (s *Store) GameRootPaths(gameID string) (map[string]string, error) {
	roots, err := s.ListGameRoots(gameID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(roots))
	for _, r := range roots {
		if r.Mapped() {
			out[r.Name] = r.Path
		}
	}
	return out, nil
}
