package syncengine

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

// Syncing a game's extra save locations.
//
// This runs as its own pass after the primary location has synced, and the
// primary path above is deliberately untouched by it. That is not tidiness:
// the primary path carries every lineage rule this project has had to fix
// more than once, and nearly every game in the world has only that one
// location. Extra locations are new, opt-in, and must not be able to change
// how a single-root game behaves — if this whole file were deleted, every
// existing game would sync exactly as it does today.

// sharedRoot is one extra location both devices have: its name, this
// device's path for it, and what the peer holds there.
type sharedRoot struct {
	root   syncRoot
	remote delta.Manifest
}

// sharedRoots works out which extra locations this sync can actually cover.
//
// Three things must all be true, and each exclusion is a correctness rule
// rather than an optimisation:
//
//   - The peer must have answered with a proto. Without it the peer is on a
//     build that ignores the root name in a block request and would serve the
//     file from its PRIMARY location instead — this side would then write one
//     folder's contents over another.
//   - This device must have a local path for the location. A name learned
//     from a peer but never pointed anywhere has nowhere to write; inventing
//     a path for someone's save data is not this program's decision to make.
//   - The peer must list the location too. If it does not, there is nothing
//     to compare against, and treating "absent" as "empty" would propagate as
//     a deletion of every file in it.
func (e *Engine) sharedRoots(gameID string, remoteData ManifestResponse) []sharedRoot {
	if remoteData.Proto < ProtoMultiRoot {
		return nil
	}
	localPaths, err := e.Store.GameRootPaths(gameID)
	if err != nil || len(localPaths) == 0 {
		return nil
	}

	var out []sharedRoot
	for name, path := range localPaths {
		remoteRoot, ok := remoteData.Manifest.Extra[name]
		if !ok {
			continue
		}
		out = append(out, sharedRoot{
			root: syncRoot{Name: name, Path: path},
			remote: delta.Manifest{
				Files:       remoteRoot.Files,
				Dirs:        remoteRoot.Dirs,
				LatestMtime: remoteData.Manifest.LatestMtime,
			},
		})
	}
	return out
}

// syncExtraRoots syncs each shared extra location independently.
//
// Independently is the point. A location that disagrees raises a conflict for
// itself and stops there; the others carry on. A config folder both devices
// edited must not hold the save folder hostage, which is what whole-game
// conflict granularity would do.
//
// Failures here never fail the sync as a whole: the primary save has already
// been dealt with by the time this runs, and reporting the game as failed
// because a mods folder was unreadable would be a lie about the thing the
// user actually cares about.
func (e *Engine) syncExtraRoots(ctx context.Context, gameID string, game store.Game, peer Peer, remoteData ManifestResponse) {
	for _, sr := range e.sharedRoots(gameID, remoteData) {
		if err := e.syncOneRoot(ctx, gameID, game, peer, sr, remoteData); err != nil {
			e.Log("warn", fmt.Sprintf("could not sync the %q location of %q with %q: %v",
				sr.root.Name, game.Name, peer.Name, err))
		}
	}
}

func (e *Engine) syncOneRoot(ctx context.Context, gameID string, game store.Game, peer Peer, sr sharedRoot, remoteData ManifestResponse) error {
	if e.hasRootConflict(gameID, sr.root.Name) {
		// Already waiting on a decision. Syncing past it would answer the
		// question on the user's behalf, which is the one thing a conflict
		// exists to stop happening.
		return nil
	}

	local, err := delta.BuildManifest(sr.root.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", sr.root.Path, err)
	}

	fileList, dirList, err := e.Store.GetSyncStateForRoot(gameID, peer.ID, sr.root.Name)
	if err != nil {
		return err
	}
	decision := Compute(local, sr.remote, toSet(fileList), toSet(dirList))

	if !decision.HasChanges() {
		e.persistRootLineage(gameID, peer.ID, sr.root.Name, local, sr.remote)
		_ = e.Store.SetAgreedHashForRoot(gameID, peer.ID, sr.root.Name, local.RootHash(delta.PrimaryRoot))
		return nil
	}

	// Divergence is judged with the SAME detector the primary location uses,
	// against this location's own merge base — so one folder disagreeing says
	// nothing about the others.
	//
	// This was originally a hand-rolled check for "a pull and a push in the
	// same decision", which is not the same question at all: when both devices
	// edit the same file, the classifier can resolve it as a pull alone, the
	// check never fires, and this side's version is silently overwritten. The
	// point of detecting a conflict is that nobody's work disappears, and a
	// second detector that is nearly right is worse than none, because it
	// looks like the work has been done.
	base := e.Store.GetAgreedHashForRoot(gameID, peer.ID, sr.root.Name)
	if DetectConflict(local, sr.remote, e.lastSyncTimeMs(peer.ID), base) {
		// Neither side is touched. There is no per-location resolution screen
		// yet, so this location simply stops syncing until the two are made to
		// agree by hand — which is the safe half of the bargain, and is said
		// out loud rather than left to be noticed.
		e.Log("warn", fmt.Sprintf(
			"both devices changed the %q save location of %q since they last agreed — waiting for a decision",
			sr.root.Name, game.Name))
		e.registerRootConflict(gameID, sr, peer, local)
		return nil
	}

	// A "root" manifest reuses the primary field names, so the response fed
	// to the pull path describes this location and nothing else — the pull
	// then resolves every path against this location's own directory.
	rootResp := ManifestResponse{
		Manifest:     sr.remote,
		ActiveBranch: remoteData.ActiveBranch,
		Proto:        remoteData.Proto,
	}

	e.applyLocalDeletions(sr.root, decision)
	e.propagateDeletions(ctx, peer, gameID, sr.root, decision)
	e.createPulledDirsIn(sr.root, decision.DirsToPull)

	if len(decision.FilesToPull) > 0 {
		if err := e.pullFiles(ctx, peer, gameID, game, sr.root, local, rootResp, decision.FilesToPull); err != nil {
			return err
		}
	}
	if decision.HasPush() {
		e.Transport.TriggerPeerPull(peer, gameID)
	}

	fresh, freshErr := delta.BuildManifest(sr.root.Path)
	if freshErr == nil {
		e.persistRootLineage(gameID, peer.ID, sr.root.Name, mergeManifestPaths(fresh, local), sr.remote)
	}

	// Same ratchet as the primary location, for the same reason: after a pure
	// pull this side holds exactly what the peer had, so that is a
	// convergence. Anything this side handed over is recorded as pushed
	// instead, to be proven by observing the peer holding it — a base that
	// never advances is a base stranded behind both sides, and that turns
	// every later one-sided edit into a false conflict.
	if !decision.HasPush() &&
		len(decision.FilesToDeleteOnPeer) == 0 && len(decision.DirsToDeleteOnPeer) == 0 {
		_ = e.Store.SetAgreedHashForRoot(gameID, peer.ID, sr.root.Name, sr.remote.RootHash(delta.PrimaryRoot))
	} else if freshErr == nil {
		_ = e.Store.SetPushedHashForRoot(gameID, peer.ID, sr.root.Name, fresh.RootHash(delta.PrimaryRoot))
	}
	return nil
}

func (e *Engine) persistRootLineage(gameID, peerID, root string, local, remote delta.Manifest) {
	files, dirs := IntersectLineage(local, remote)
	if err := e.Store.SetSyncStateForRoot(gameID, peerID, root, files, dirs); err != nil {
		e.Log("warn", fmt.Sprintf("persist lineage for the %q location failed: %v", root, err))
	}
}

// contentHashOf is the "has anything about this game changed" value, spanning
// every save location it has.
//
// It must agree with what the watcher records, because the two are compared
// against each other: the watcher stores this after an auto-snapshot, and the
// sync engine reads it back to decide whether the local save holds changes no
// snapshot has captured. If one side counted the extra locations and the other
// did not, a multi-location game would look like it had uncaptured changes
// forever, and every single pull would raise a conflict over nothing.
//
// For a game with one folder it is exactly ManifestHash, so nothing recorded
// before locations existed is invalidated.
func (e *Engine) contentHashOf(gameID string, primary delta.Manifest, savePath string) string {
	rules := e.rulesFor(gameID)
	extra, err := e.Store.GameRootPaths(gameID)
	if err != nil || len(extra) == 0 {
		// Filtered for the same reason the watcher filters: the two values are
		// compared against each other, and a mismatch here reads as "the save
		// holds changes a pull would overwrite" — which a file nobody syncs
		// never does.
		return filterManifest(primary, rules).ManifestHash()
	}
	full, _, err := delta.BuildMultiManifest(savePath, extra)
	if err != nil {
		// Fall back to the primary-only value rather than a wrong one: it can
		// cost a spurious conflict prompt, which is recoverable, where a made-up
		// hash silently overwrites.
		return primary.ManifestHash()
	}
	return filterManifest(full, rules).ContentHash()
}
