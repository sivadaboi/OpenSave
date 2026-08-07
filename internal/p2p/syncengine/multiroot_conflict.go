package syncengine

import (
	"context"
	"fmt"
	"sort"

	"github.com/opensave/opensave/internal/delta"
)

// Divergence in an extra save location.
//
// Kept apart from the primary location's conflict machinery, and keyed
// separately, because the two are answered differently. "Keep both" parks the
// peer's copy on a branch, and branches belong to the game as a whole — there
// is no such thing as branching one folder of it. Offering it here would be a
// button that quietly did something to the save folder while the user was
// looking at a settings folder.

// RootConflict is one save location the two devices disagree about.
type RootConflict struct {
	GameID string `json:"gameId"`
	Root   string `json:"root"`
	Peer   Peer   `json:"peer"`

	LocalStats  SideStats  `json:"localStats"`
	RemoteStats SideStats  `json:"remoteStats"`
	DiffFiles   []DiffFile `json:"diffFiles"`
	DiffTotal   int        `json:"diffTotal"`
}

// rootConflictKey identifies one. A game can have several locations diverge
// independently, which is the entire point of per-location lineage.
func rootConflictKey(gameID, root string) string { return gameID + "\x00" + root }

// ActiveRootConflicts returns the diverged locations awaiting a decision.
func (e *Engine) ActiveRootConflicts() []RootConflict {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]RootConflict, 0, len(e.rootConflicts))
	for _, c := range e.rootConflicts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GameID != out[j].GameID {
			return out[i].GameID < out[j].GameID
		}
		return out[i].Root < out[j].Root
	})
	return out
}

func (e *Engine) registerRootConflict(gameID string, sr sharedRoot, peer Peer, local delta.Manifest) {
	diffs := diffManifests(local, sr.remote)
	const maxDiffFiles = 100
	total := len(diffs)
	if len(diffs) > maxDiffFiles {
		diffs = diffs[:maxDiffFiles]
	}

	e.mu.Lock()
	if e.rootConflicts == nil {
		e.rootConflicts = map[string]*RootConflict{}
	}
	e.rootConflicts[rootConflictKey(gameID, sr.root.Name)] = &RootConflict{
		GameID: gameID, Root: sr.root.Name, Peer: peer,
		LocalStats:  manifestStats(local),
		RemoteStats: manifestStats(sr.remote),
		DiffFiles:   diffs,
		DiffTotal:   total,
	}
	e.mu.Unlock()

	if e.Progress.OnConflict != nil {
		e.Progress.OnConflict(gameID)
	}
}

func (e *Engine) clearRootConflict(gameID, root string) {
	e.mu.Lock()
	delete(e.rootConflicts, rootConflictKey(gameID, root))
	e.mu.Unlock()
	if e.Progress.OnConflict != nil {
		e.Progress.OnConflict(gameID)
	}
}

func (e *Engine) hasRootConflict(gameID, root string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.rootConflicts[rootConflictKey(gameID, root)]
	return ok
}

// ResolveRootConflict applies the user's choice to one save location.
//
// Only this device's copy of that one folder is ever touched. As with the
// primary location, neither answer reaches over and rewrites the peer:
// "keep mine" asks the peer to pull, and the peer's own engine decides what
// to do with that, including raising its own question if it has work of its
// own.
//
// A snapshot is taken first either way. That snapshot covers every location
// of the game, so it is a real undo for a choice made about one of them.
func (e *Engine) ResolveRootConflict(ctx context.Context, gameID, peerID, root, resolution string) error {
	e.mu.Lock()
	conflict := e.rootConflicts[rootConflictKey(gameID, root)]
	e.mu.Unlock()
	if conflict == nil || conflict.Peer.ID != peerID {
		return fmt.Errorf("no active conflict for the %q location of game %q", root, gameID)
	}
	peer := conflict.Peer

	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return err
	}
	paths, err := e.Store.GameRootPaths(gameID)
	if err != nil {
		return err
	}
	path, ok := paths[root]
	if !ok {
		return fmt.Errorf("this device no longer has a folder for the %q location", root)
	}

	comment := fmt.Sprintf("Before resolving the %q save location with %s", root, peer.Name)
	if _, err := e.Snapshots.Create(gameID, comment, true); err != nil {
		e.Log("warn", fmt.Sprintf("safety snapshot before resolving a location failed: %v", err))
	}

	switch resolution {
	case "keep-local":
		// Our copy becomes the shared state: record it as the agreed base so
		// this exact divergence cannot re-prompt, then ask the peer to pull.
		local, buildErr := delta.BuildManifest(path)
		if buildErr != nil {
			return buildErr
		}
		if err := e.Store.SetAgreedHashForRoot(gameID, peerID, root, local.RootHash(delta.PrimaryRoot)); err != nil {
			return err
		}
		files, dirs := IntersectLineage(local, local)
		_ = e.Store.SetSyncStateForRoot(gameID, peerID, root, files, dirs)
		e.clearRootConflict(gameID, root)
		e.Transport.TriggerPeerPull(peer, gameID)
		e.Log("info", fmt.Sprintf("the %q save location of %q resolved: this device's version becomes the shared one",
			root, game.Name))
		return nil

	case "keep-remote":
		// Adopt the peer's copy of this folder. The lineage is cleared first
		// so the pull that follows is judged as a fresh start rather than
		// against the base the two just disagreed about — leaving it would
		// re-detect the same divergence and refuse to move.
		if err := e.Store.SetAgreedHashForRoot(gameID, peerID, root, ""); err != nil {
			return err
		}
		_ = e.Store.SetSyncStateForRoot(gameID, peerID, root, nil, nil)
		e.clearRootConflict(gameID, root)

		remoteData, fetchErr := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{
			Name: game.Name, SavePath: game.SavePath, AppID: game.AppID, CoverURL: game.CoverURL,
		})
		if fetchErr != nil {
			return fmt.Errorf("fetch the peer's copy: %w", fetchErr)
		}
		remoteRoot, has := remoteData.Manifest.Extra[root]
		if !has {
			return fmt.Errorf("the peer no longer has a %q save location", root)
		}
		sr := sharedRoot{
			root:   syncRoot{Name: root, Path: path},
			remote: delta.Manifest{Files: remoteRoot.Files, Dirs: remoteRoot.Dirs},
		}
		e.Log("info", fmt.Sprintf("the %q save location of %q resolved: adopting %s's version",
			root, game.Name, peer.Name))
		return e.syncOneRoot(ctx, gameID, game, peer, sr, remoteData)
	}
	return fmt.Errorf("unknown resolution %q", resolution)
}
