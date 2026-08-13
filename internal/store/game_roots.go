package store

import (
	"fmt"
	slashpath "path"
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
//
// The comparison is deliberately not the host's. It used os.PathSeparator and
// filepath, which means a Windows path is only understood when Windows is
// running: on Linux, filepath.Abs turns `C:\Saves\ER` into a single filename
// under the working directory, `C:\Saves\ER\config` into a different one, and
// neither is a prefix of the other — so every overlap is missed and the guard
// silently passes everything. That is how it reads on a Linux CI runner today.
//
// A check that exists to prevent two locations owning the same file should not
// depend on which machine it runs on, so both sides are reduced to one
// convention first and compared as text.
func overlaps(a, b string) bool {
	pa, pb := normalizeLocationPath(a), normalizeLocationPath(b)
	if pa == "" || pb == "" {
		return false
	}
	if pa == pb {
		return true
	}
	return pathContains(pa, pb) || pathContains(pb, pa)
}

// pathContains reports whether child sits underneath parent. The separator is part
// of the test on purpose: without it "/games/ab" reads as being inside
// "/games/a", and two unrelated folders would refuse each other.
func pathContains(parent, child string) bool {
	if parent == "/" {
		return strings.HasPrefix(child, "/")
	}
	return strings.HasPrefix(child, parent+"/")
}

// normalizeLocationPath reduces a path to lowercase, forward-slashed, cleaned
// form so two of them can be compared as strings on any operating system.
//
// Lowercasing is not right on Linux, where paths are case-sensitive, and it is
// kept anyway: the cost of being wrong is refusing a location that did not
// really overlap, which the user sees and can rename around. The cost the
// other way is two locations owning one file, each sync patching it twice and
// deletions bouncing back and forth. Between an annoyance and corruption, the
// guard leans on the annoyance.
func normalizeLocationPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, `\`, "/")

	// Only resolve against the working directory when the path is not already
	// absolute in either convention. Calling filepath.Abs on `C:/Saves` from
	// Linux is precisely the bug above: it produces a path under the cwd and
	// destroys the relationship being tested.
	if !isAbsoluteAnyOS(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = strings.ReplaceAll(abs, `\`, "/")
		}
	}

	// path.Clean, not filepath.Clean: the slash-based one behaves the same
	// everywhere, which is the entire point here.
	return strings.ToLower(slashpath.Clean(p))
}

// isAbsoluteAnyOS reports whether a slash-normalised path is already anchored,
// under either Unix or Windows rules — a leading slash, a UNC share, or a
// drive letter.
func isAbsoluteAnyOS(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true // includes //server/share
	}
	if len(p) >= 2 && p[1] == ':' {
		if c := p[0] | 0x20; c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
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

// NoteGameRoot records that a location by this name exists, WITHOUT touching
// where it lives if this device already knows.
//
// This is the difference between learning a name and setting a path, and
// conflating the two is destructive: an archive or a peer mentioning
// "config" would otherwise blank the folder the user had already chosen for
// it, so the next sync or restore would skip the location entirely and the
// files would appear to vanish. Use AddGameRoot to set a path deliberately;
// use this to learn that a name exists.
func (s *Store) NoteGameRoot(gameID, name string) error {
	n := NormalizeRootName(name)
	if n == "" {
		return fmt.Errorf("a save location needs a name")
	}
	_, err := s.db.Exec(`
		INSERT INTO game_roots (game_id, name, path, ordinal)
		VALUES (?, ?, '', COALESCE((SELECT MAX(ordinal) + 1 FROM game_roots WHERE game_id = ?), 0))
		ON CONFLICT(game_id, name) DO NOTHING`,
		gameID, n, gameID)
	if err != nil {
		return fmt.Errorf("note game root: %w", err)
	}
	return nil
}
