// Package syncengine implements the P2P sync state machine ported from
// src/daemon/p2p/sync-engine.js: lineage-based direction/conflict
// classification (never wall-clock alone), block-level concurrent pulls
// with bandwidth throttling, deletion propagation, and the three conflict
// resolutions (keep-local / keep-remote / merge-branch).
//
// This file holds the PURE decision logic — no I/O — so the exact
// classification semantics are locked down by table-driven tests.
package syncengine

import (
	"github.com/opensave/opensave/internal/delta"
)

// clockSkewToleranceMs matches the JS engine's 2-second allowance when
// comparing manifest mtimes against the last sync time.
const clockSkewToleranceMs = 2000

// DetectConflict reports whether both sides changed since the last sync —
// the only situation that needs user intervention.
//
// Rules (ported exactly):
//   - Identical manifests are never a conflict.
//   - Never synced before (lastSyncTimeMs == 0): conflict only if BOTH
//     sides already have files (two pre-existing, diverged save states).
//   - Otherwise: conflict when both manifests' latest mtimes are newer
//     than lastSyncTimeMs + 2s of skew tolerance.
func DetectConflict(local, remote delta.Manifest, lastSyncTimeMs int64, agreedHash string) bool {
	if local.ManifestHash() == remote.ManifestHash() {
		return false
	}
	// Content-based detection when a convergence point is known (the
	// manifest hash both sides verifiably held — a merge-base): conflict
	// only when BOTH sides changed relative to it. No clocks involved, so
	// no skew tolerance and no blind window right after a sync.
	if agreedHash != "" {
		return local.ManifestHash() != agreedHash && remote.ManifestHash() != agreedHash
	}
	// Legacy fallback (no convergence recorded yet): mtimes vs last sync.
	if lastSyncTimeMs == 0 {
		return len(local.Files) > 0 && len(remote.Files) > 0
	}
	localModified := int64(local.LatestMtime) > lastSyncTimeMs+clockSkewToleranceMs
	remoteModified := int64(remote.LatestMtime) > lastSyncTimeMs+clockSkewToleranceMs
	return localModified && remoteModified
}

// Decision is the complete plan for one game/peer sync run.
type Decision struct {
	FilesToPull          []string // exists (or newer) on remote -> download
	FilesToPush          []string // exists (or newer) locally -> trigger peer pull
	FilesToDeleteOnPeer  []string // we deleted since last sync -> propagate
	FilesToDeleteLocally []string // peer deleted since last sync -> apply
	DirsToPull           []string
	DirsToPush           []string
	DirsToDeleteOnPeer   []string
	DirsToDeleteLocally  []string
}

// HasChanges reports whether anything at all needs to happen.
func (d Decision) HasChanges() bool {
	return len(d.FilesToPull) > 0 || len(d.FilesToPush) > 0 ||
		len(d.FilesToDeleteOnPeer) > 0 || len(d.FilesToDeleteLocally) > 0 ||
		len(d.DirsToPull) > 0 || len(d.DirsToPush) > 0 ||
		len(d.DirsToDeleteOnPeer) > 0 || len(d.DirsToDeleteLocally) > 0
}

// HasPull / HasPush / HasDeletions mirror the JS result classification.
func (d Decision) HasPull() bool { return len(d.FilesToPull) > 0 || len(d.DirsToPull) > 0 }
func (d Decision) HasPush() bool { return len(d.FilesToPush) > 0 || len(d.DirsToPush) > 0 }
func (d Decision) HasDeletions() bool {
	return len(d.FilesToDeleteOnPeer) > 0 || len(d.FilesToDeleteLocally) > 0 ||
		len(d.DirsToDeleteOnPeer) > 0 || len(d.DirsToDeleteLocally) > 0
}

// Compute classifies every file and directory across both manifests using
// the per-peer lineage sets (paths present at the last successful sync).
// The lineage is what distinguishes "new on remote" (pull it) from
// "deleted locally" (propagate the delete) — pure set logic, no clocks.
// Only the modified-both-sides case falls back to mtime comparison, with
// remote winning ties.
func Compute(local, remote delta.Manifest, lastSyncedFiles, lastSyncedDirs map[string]struct{}) Decision {
	var d Decision

	allFiles := map[string]struct{}{}
	for p := range local.Files {
		allFiles[p] = struct{}{}
	}
	for p := range remote.Files {
		allFiles[p] = struct{}{}
	}

	for relPath := range allFiles {
		localFile, hasLocal := local.Files[relPath]
		remoteFile, hasRemote := remote.Files[relPath]

		switch {
		case hasRemote && !hasLocal:
			if _, synced := lastSyncedFiles[relPath]; synced {
				d.FilesToDeleteOnPeer = append(d.FilesToDeleteOnPeer, relPath)
			} else {
				d.FilesToPull = append(d.FilesToPull, relPath)
			}

		case hasLocal && !hasRemote:
			if _, synced := lastSyncedFiles[relPath]; synced {
				d.FilesToDeleteLocally = append(d.FilesToDeleteLocally, relPath)
			} else {
				d.FilesToPush = append(d.FilesToPush, relPath)
			}

		case hasLocal && hasRemote && localFile.Hash != remoteFile.Hash:
			switch {
			case remoteFile.MtimeMs > localFile.MtimeMs:
				d.FilesToPull = append(d.FilesToPull, relPath)
			case localFile.MtimeMs > remoteFile.MtimeMs:
				d.FilesToPush = append(d.FilesToPush, relPath)
			default: // tie: pull from remote
				d.FilesToPull = append(d.FilesToPull, relPath)
			}
		}
	}

	localDirSet := toSet(local.Dirs)
	remoteDirSet := toSet(remote.Dirs)
	allDirs := map[string]struct{}{}
	for p := range localDirSet {
		allDirs[p] = struct{}{}
	}
	for p := range remoteDirSet {
		allDirs[p] = struct{}{}
	}

	for relDir := range allDirs {
		_, hasLocal := localDirSet[relDir]
		_, hasRemote := remoteDirSet[relDir]

		switch {
		case hasRemote && !hasLocal:
			if _, synced := lastSyncedDirs[relDir]; synced {
				d.DirsToDeleteOnPeer = append(d.DirsToDeleteOnPeer, relDir)
			} else {
				d.DirsToPull = append(d.DirsToPull, relDir)
			}
		case hasLocal && !hasRemote:
			if _, synced := lastSyncedDirs[relDir]; synced {
				d.DirsToDeleteLocally = append(d.DirsToDeleteLocally, relDir)
			} else {
				d.DirsToPush = append(d.DirsToPush, relDir)
			}
		}
	}

	return d
}

// DifferentBlockIndices returns which block indices of a remote file need
// fetching, given the local counterpart (if any): everything when the file
// is new locally or block sizes differ; otherwise indices whose hashes
// differ (including length mismatches in either direction).
func DifferentBlockIndices(localFile *delta.FileEntry, remoteFile delta.FileEntry) []int {
	if localFile == nil || localFile.BlockSize != remoteFile.BlockSize {
		indices := make([]int, len(remoteFile.Blocks))
		for i := range indices {
			indices[i] = i
		}
		return indices
	}

	maxBlocks := len(remoteFile.Blocks)
	if len(localFile.Blocks) > maxBlocks {
		maxBlocks = len(localFile.Blocks)
	}
	var indices []int
	for i := 0; i < maxBlocks; i++ {
		if i >= len(remoteFile.Blocks) {
			break // local-only trailing blocks: nothing to fetch, patch truncates
		}
		if i >= len(localFile.Blocks) || localFile.Blocks[i].Hash != remoteFile.Blocks[i].Hash {
			indices = append(indices, i)
		}
	}
	return indices
}

// BatchIndices splits block indices into fetch batches sized so a JSON
// response stays under ~1.5MB (relay/proxy payload limits), capped at 8
// blocks per batch on WAN and 16 on LAN — exactly the JS math.
func BatchIndices(indices []int, blockSize int, isWan bool) [][]int {
	if blockSize <= 0 {
		blockSize = 64 * 1024
	}
	const targetBatchBytes = 3 * 512 * 1024 // 1.5 MB
	calculated := targetBatchBytes / blockSize
	if calculated < 1 {
		calculated = 1
	}
	cap := 16
	if isWan {
		cap = 8
	}
	batchSize := calculated
	if batchSize > cap {
		batchSize = cap
	}

	var batches [][]int
	for i := 0; i < len(indices); i += batchSize {
		end := i + batchSize
		if end > len(indices) {
			end = len(indices)
		}
		batches = append(batches, indices[i:end])
	}
	return batches
}

// ConcurrencyFor returns how many batches to fetch at once: 3 on WAN, 5 on
// LAN.
func ConcurrencyFor(isWan bool) int {
	if isWan {
		return 3
	}
	return 5
}

func toSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}
