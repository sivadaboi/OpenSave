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
		// Keep our version as the resolved state. We record it as the agreed
		// merge-base (so this exact divergence never re-prompts) and ask the
		// peer to pull our version — its own engine applies it with a safety
		// snapshot, or re-raises the conflict if the peer has NEW unsynced
		// work of its own. Without recording the base, the next sync would
		// see both sides still differing from the stale base and re-conflict
		// immediately — the "keep looping" bug.
		e.Log("info", fmt.Sprintf("conflict on %q resolved: keep LOCAL — our version becomes the shared state", gameID))
		if err := e.markResolvedLocal(gameID, peer); err != nil {
			return "", err
		}
		e.clearConflict(gameID)
		e.Transport.TriggerPeerPull(peer, gameID)
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
		e.markResolvedConverged(gameID, peer)
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
		e.markResolvedConverged(gameID, peer)
		e.clearConflict(gameID)
		return branchName, nil

	default:
		return "", fmt.Errorf("invalid conflict resolution %q", resolution)
	}
}

// markResolvedConverged records the post-resolution convergence for the
// keep-remote / keep-both paths, where the local save was just overwritten
// to match the peer. It captures the CURRENT local manifest as the agreed
// merge-base and lineage, and tells the peer we are now in sync at that
// hash — the peer, already holding that exact state, re-confirms identity
// on its own clock (ConfirmInSync) and records the same base. Both sides
// end at one merge-base, so the resolved divergence can never re-trigger.
//
// Capturing the manifest AFTER the overwrite (rather than assuming it
// equals the remote) means even a partially-applied overwrite is safe: the
// base matches our real files, so the next sync finishes converging via a
// normal pull instead of re-raising a conflict.
func (e *Engine) markResolvedConverged(gameID string, peer Peer) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return
	}
	local, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		e.Log("warn", fmt.Sprintf("record resolution: build manifest failed: %v", err))
		return
	}
	e.persistLineage(gameID, peer.ID, local, local)
	_ = e.Store.SetAgreedHash(gameID, peer.ID, local.ManifestHash())
	_ = e.Store.UpdatePeerLastSynced(peer.ID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	_ = e.Store.SetLastManifestHash(gameID, local.ManifestHash())
	e.Transport.ReportSyncEvent(peer, gameID, "in-sync", map[string]any{
		"peerName":     e.deviceName(),
		"manifestHash": local.ManifestHash(),
	})
}

// markResolvedLocal records our local version as the agreed merge-base for
// the keep-local path (we did NOT overwrite anything) and makes it
// authoritative so it propagates to the peer instead of being pulled back.
//
// Subtlety: recording the merge-base alone is not enough. On the next
// sync the file still differs on both sides, so Compute falls to its
// mtime tiebreak — and if the peer's copy happens to carry a newer mtime,
// OUR version would be pulled away, silently undoing the user's "keep
// mine" choice. So we refresh our save files' mtimes to now: the content
// (and thus the merge-base hash) is unchanged, but our side is now
// unambiguously the newest, so the sync direction is a push. The peer
// then receives our version (its own engine snapshots first, or re-raises
// its own conflict if it has newer unsynced work of its own).
func (e *Engine) markResolvedLocal(gameID string, peer Peer) error {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return err
	}
	touchSaveMtimes(game.SavePath)
	local, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return err
	}
	e.persistLineage(gameID, peer.ID, local, local)
	_ = e.Store.SetAgreedHash(gameID, peer.ID, local.ManifestHash())
	_ = e.Store.SetLastManifestHash(gameID, local.ManifestHash())
	_ = e.Store.UpdatePeerLastSynced(peer.ID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	return nil
}

// touchSaveMtimes bumps every file's mtime under root to now without
// changing its content, marking this side as the most recent version.
func touchSaveMtimes(root string) {
	now := time.Now()
	info, err := os.Stat(root)
	if err != nil {
		return
	}
	if !info.IsDir() {
		_ = os.Chtimes(root, now, now)
		return
	}
	_ = filepath.Walk(root, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil || fi.IsDir() {
			return nil
		}
		_ = os.Chtimes(path, now, now)
		return nil
	})
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
