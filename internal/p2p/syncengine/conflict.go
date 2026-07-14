package syncengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
)

// ResolveConflict applies the user's chosen resolution to an active
// conflict. Every resolution affects ONLY this device's save — it never
// reaches over and rewrites the peer's save without the peer's own user
// choosing. The peer resolves its own copy of the conflict independently.
//
//   - keep-local:   keep this device's version as-is (peer is untouched).
//   - keep-remote:  adopt the peer's current state on this device, taking a
//     safety snapshot of the local version first.
//   - merge-branch: park the peer's state on a new "conflict-<peer>-<id>"
//     branch so both versions survive; switch between them later.
func (e *Engine) ResolveConflict(ctx context.Context, gameID, peerID, resolution string) (branchName string, err error) {
	e.mu.Lock()
	conflict := e.activeConflicts[gameID]
	e.mu.Unlock()
	if conflict == nil || conflict.Peer.ID != peerID {
		return "", fmt.Errorf("no active conflict for game %q and peer %q", gameID, peerID)
	}
	peer := conflict.Peer

	switch resolution {
	case "keep-local":
		// Purely local: keep our version, clear our conflict. We deliberately
		// do NOT force the peer to adopt our save — that would change their
		// files without their consent. If they want ours, their own engine
		// detects the same conflict and their user chooses "keep theirs".
		e.Log("info", fmt.Sprintf("conflict on %q resolved: keep LOCAL (peer's save left untouched)", gameID))
		e.clearConflict(gameID)
		return "", nil

	case "keep-remote":
		e.Log("info", fmt.Sprintf("conflict on %q resolved: keep REMOTE — overwriting local", gameID))
		// Non-destructive: snapshot the local version first so "keep theirs"
		// can always be undone from the Snapshots tab. (merge-branch gets
		// this for free via SwitchBranch's safety snapshot; this path
		// otherwise wouldn't.)
		if _, err := e.Snapshots.Create(gameID, fmt.Sprintf("This device's version (before keeping %s's)", peer.Name), true); err != nil {
			e.Log("warn", fmt.Sprintf("safety snapshot before keep-remote failed: %v", err))
		}
		if err := e.overwriteLocalWithRemote(ctx, gameID, peer, "Resolved conflict: Overwrite with remote"); err != nil {
			return "", err
		}
		e.clearConflict(gameID)
		return "", nil

	case "merge-branch":
		branchName = fmt.Sprintf("conflict-%s-%s",
			snapshot.CleanBranchName(peer.Name),
			lastN(fmt.Sprintf("%d", time.Now().UnixMilli()), 4))
		e.Log("info", fmt.Sprintf("conflict on %q resolved: keep BOTH — remote goes to branch %q", gameID, branchName))

		if _, err := e.Snapshots.CreateBranch(gameID, branchName); err != nil {
			return "", err
		}
		if err := e.Snapshots.SwitchBranch(gameID, branchName); err != nil {
			return "", err
		}
		if err := e.overwriteLocalWithRemote(ctx, gameID, peer, "Diverged save state from peer: "+peer.Name); err != nil {
			return "", err
		}
		e.clearConflict(gameID)
		return branchName, nil

	default:
		return "", fmt.Errorf("invalid conflict resolution %q", resolution)
	}
}

func (e *Engine) clearConflict(gameID string) {
	e.mu.Lock()
	delete(e.activeConflicts, gameID)
	e.mu.Unlock()
	if e.Progress.OnConflict != nil {
		e.Progress.OnConflict(gameID) // state changed; let the UI refresh
	}
}

// overwriteLocalWithRemote makes the local save byte-identical to the
// peer's current state: delete local-only files, pull every added/changed
// file, then mirror the peer's latest snapshot.
func (e *Engine) overwriteLocalWithRemote(ctx context.Context, gameID string, peer Peer, mirrorComment string) error {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return err
	}

	remoteData, err := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{Name: game.Name, SavePath: game.SavePath, AppID: game.AppID, CoverURL: game.CoverURL})
	if err != nil {
		return fmt.Errorf("fetch remote manifest: %w", err)
	}
	localManifest, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return err
	}

	// Local-only files are deleted (remote is the source of truth here).
	for relPath := range localManifest.Files {
		if _, onRemote := remoteData.Manifest.Files[relPath]; onRemote {
			continue
		}
		if !delta.IsSafePath(game.SavePath, relPath) {
			continue
		}
		full := filepath.Join(game.SavePath, filepath.FromSlash(relPath))
		_ = os.Chmod(full, 0o666)
		_ = os.Remove(full)
	}

	// Pull everything that's new or different.
	var filesToPull []string
	for relPath, remoteFile := range remoteData.Manifest.Files {
		if localFile, ok := localManifest.Files[relPath]; ok && localFile.Hash == remoteFile.Hash {
			continue
		}
		filesToPull = append(filesToPull, relPath)
	}

	if len(filesToPull) > 0 {
		if err := e.pullFiles(ctx, peer, gameID, game, localManifest, remoteData, filesToPull); err != nil {
			return err
		}
	} else if remoteData.LatestSnapshot != nil {
		// Nothing to transfer but still record the shared history entry.
		e.recordMirrorSnapshot(gameID, game, peer, *remoteData.LatestSnapshot,
			fmt.Sprintf("Synced from peer: %s (%s)", peer.Name, mirrorComment))
	}
	return nil
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
