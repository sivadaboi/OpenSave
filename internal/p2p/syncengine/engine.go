package syncengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// Conflict is a diverged-save state awaiting user resolution.
type Conflict struct {
	Peer       Peer         `json:"peer"`
	LocalSnap  SnapshotInfo `json:"localSnap"`
	RemoteSnap SnapshotInfo `json:"remoteSnap"`
	// Comparison data so the user can make an informed choice.
	LocalStats  SideStats  `json:"localStats"`
	RemoteStats SideStats  `json:"remoteStats"`
	DiffFiles   []DiffFile `json:"diffFiles"` // capped; DiffTotal is the real count
	DiffTotal   int        `json:"diffTotal"`
}

// SideStats summarises one side's save state for the conflict UI.
type SideStats struct {
	Files         int   `json:"files"`
	TotalBytes    int64 `json:"totalBytes"`
	LatestMtimeMs int64 `json:"latestMtimeMs"`
}

// DiffFile is one path that differs between the two sides. Sizes are -1
// when the file doesn't exist on that side.
type DiffFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // changed | only-local | only-remote
	LocalSize  int64  `json:"localSize"`
	RemoteSize int64  `json:"remoteSize"`
}

// Result summarizes one game/peer sync run.
type Result struct {
	Status    string `json:"status"` // in_sync | updated | updated_bidirectional | deletions_synced | triggered_peer_pull | conflict
	Direction string `json:"direction"`
	PeerID    string `json:"peerId,omitempty"`
	PeerName  string `json:"peerName,omitempty"`
}

// Engine orchestrates sync runs. Construct with New.
type Engine struct {
	Store     *store.Store
	Snapshots *snapshot.Manager
	Transport Transport
	Progress  ProgressCallbacks
	Log       func(level, msg string)

	mu              sync.Mutex
	activeSyncs     map[string]bool
	activeConflicts map[string]*Conflict
}

// New creates an Engine.
func New(s *store.Store, snaps *snapshot.Manager, transport Transport) *Engine {
	return &Engine{
		Store:           s,
		Snapshots:       snaps,
		Transport:       transport,
		Log:             func(string, string) {},
		activeSyncs:     map[string]bool{},
		activeConflicts: map[string]*Conflict{},
	}
}

// ActiveConflicts returns a snapshot of unresolved conflicts by game id.
func (e *Engine) ActiveConflicts() map[string]Conflict {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Conflict, len(e.activeConflicts))
	for id, c := range e.activeConflicts {
		out[id] = *c
	}
	return out
}

// SyncGame syncs one game with every online paired peer. Concurrent calls
// for the same game are coalesced into a skip, same as the JS activeSyncs
// guard.
func (e *Engine) SyncGame(ctx context.Context, gameID string, onlinePeers []Peer) (map[string]Result, error) {
	e.mu.Lock()
	if e.activeSyncs[gameID] {
		e.mu.Unlock()
		return nil, fmt.Errorf("sync already running for %s", gameID)
	}
	e.activeSyncs[gameID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.activeSyncs, gameID)
		e.mu.Unlock()
	}()

	results := map[string]Result{}
	for _, peer := range onlinePeers {
		res, err := e.SyncWithPeer(ctx, gameID, peer)
		if err != nil {
			e.Log("error", fmt.Sprintf("sync %s with %s failed: %v", gameID, peer.Name, err))
			results[peer.ID] = Result{Status: "error", PeerID: peer.ID, PeerName: peer.Name}
			continue
		}
		results[peer.ID] = res
		// Only a genuinely completed sync advances the "last synced" baseline.
		// Advancing it on a conflict (or error) would hide the still-unresolved
		// divergence from the NEXT sync, causing the peer to silently overwrite
		// its own changes instead of detecting the conflict and asking.
		if res.Status != "conflict" && res.Status != "error" {
			_ = e.Store.UpdatePeerLastSynced(peer.ID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
		}
	}
	return results, nil
}

// SyncWithPeer runs the full state machine against a single peer.
func (e *Engine) SyncWithPeer(ctx context.Context, gameID string, peer Peer) (Result, error) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return Result{}, err
	}
	e.Log("info", fmt.Sprintf("syncing %q with %q (%s)", game.Name, peer.Name,
		map[bool]string{true: "WAN relay", false: "direct LAN"}[peer.Wan()]))

	// 1. Fetch remote manifest + branch info.
	isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath)
	remoteData, err := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{
		Name: game.Name, SavePath: game.SavePath, IsFile: isFile,
		AppID: game.AppID, CoverURL: game.CoverURL,
	})
	if err != nil {
		return Result{}, fmt.Errorf("fetch remote manifest: %w", err)
	}

	// 2. Branch alignment: local follows the remote's active branch.
	if remoteData.ActiveBranch != "" && game.ActiveBranch != remoteData.ActiveBranch {
		e.Log("warn", fmt.Sprintf("branch mismatch on %q: local %q vs remote %q — switching local",
			game.Name, game.ActiveBranch, remoteData.ActiveBranch))
		if _, err := e.Snapshots.CreateBranch(gameID, remoteData.ActiveBranch); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			return Result{}, err
		}
		if err := e.Snapshots.SwitchBranch(gameID, remoteData.ActiveBranch); err != nil {
			return Result{}, err
		}
		game, err = e.Store.GetGame(gameID)
		if err != nil {
			return Result{}, err
		}
	}

	localManifest, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return Result{}, fmt.Errorf("build local manifest: %w", err)
	}

	// 3. Existing unresolved conflict blocks further syncing.
	e.mu.Lock()
	if existing := e.activeConflicts[gameID]; existing != nil {
		e.mu.Unlock()
		return Result{Status: "conflict", PeerID: peer.ID, PeerName: peer.Name}, nil
	}
	e.mu.Unlock()

	// 4. Conflict detection (lineage + skew-tolerant mtimes).
	lastSyncMs := e.lastSyncTimeMs(peer.ID)
	if DetectConflict(localManifest, remoteData.Manifest, lastSyncMs) {
		e.registerConflict(gameID, peer, localManifest, remoteData)
		return Result{Status: "conflict", PeerID: peer.ID, PeerName: peer.Name}, nil
	}

	// 5. Classification.
	lineageFiles, lineageDirs, err := e.lineageSets(gameID, peer.ID)
	if err != nil {
		return Result{}, err
	}
	decision := Compute(localManifest, remoteData.Manifest, lineageFiles, lineageDirs)

	if !decision.HasChanges() {
		e.Log("success", fmt.Sprintf("%q already in sync with %q", game.Name, peer.Name))
		e.persistLineage(gameID, peer.ID, localManifest, remoteData.Manifest)
		return Result{Status: "in_sync", Direction: "none"}, nil
	}

	// Race-free safety net: the mtime-based conflict check above can miss a
	// divergence under clock skew / sync races. If we're about to overwrite
	// local files while the local save has changes that aren't captured in
	// any snapshot yet, those unsaved changes would be lost. Surface it as a
	// conflict for the user to resolve instead of silently overwriting.
	if len(decision.FilesToPull) > 0 && game.LastManifestHash != "" &&
		localManifest.ManifestHash() != game.LastManifestHash {
		e.Log("warn", fmt.Sprintf("uncaptured local changes on %q would be overwritten by %q — raising conflict", game.Name, peer.Name))
		e.registerConflict(gameID, peer, localManifest, remoteData)
		return Result{Status: "conflict", PeerID: peer.ID, PeerName: peer.Name}, nil
	}

	// 6. Apply deletions (locally + propagate to peer).
	e.applyLocalDeletions(game, decision)
	e.propagateDeletions(ctx, peer, gameID, game, decision)

	// 7. Create pulled directories (parents first).
	e.createPulledDirs(game, decision.DirsToPull)

	// 8. Pull changed files.
	if len(decision.FilesToPull) > 0 {
		if err := e.pullFiles(ctx, peer, gameID, game, localManifest, remoteData, decision.FilesToPull); err != nil {
			return Result{}, err
		}
	}

	// 9. Trigger a reciprocal pull when we hold newer content.
	if decision.HasPush() {
		e.Log("info", fmt.Sprintf("local has newer content; triggering %q to pull", peer.Name))
		e.Transport.TriggerPeerPull(peer, gameID)
	}

	// 10. Record the post-sync lineage.
	freshManifest, err := delta.BuildManifest(game.SavePath)
	if err == nil {
		e.persistLineage(gameID, peer.ID, freshManifest, remoteData.Manifest)
	}

	return e.classifyResult(decision), nil
}

func (e *Engine) classifyResult(d Decision) Result {
	switch {
	case d.HasPull() && d.HasPush():
		return Result{Status: "updated_bidirectional", Direction: "bidirectional"}
	case d.HasPull():
		return Result{Status: "updated", Direction: "pull"}
	case d.HasDeletions() && !d.HasPush():
		return Result{Status: "deletions_synced", Direction: "none"}
	default:
		return Result{Status: "triggered_peer_pull", Direction: "push"}
	}
}

func (e *Engine) lastSyncTimeMs(peerID string) int64 {
	peer, err := e.Store.GetPeer(peerID)
	if err != nil || !peer.LastSynced.Valid || peer.LastSynced.String == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", peer.LastSynced.String)
	if err != nil {
		if t2, err2 := time.Parse(time.RFC3339, peer.LastSynced.String); err2 == nil {
			return t2.UnixMilli()
		}
		return 0
	}
	return t.UnixMilli()
}

func (e *Engine) lineageSets(gameID, peerID string) (files, dirs map[string]struct{}, err error) {
	fileList, dirList, err := e.Store.GetSyncState(gameID, peerID)
	if err != nil {
		return nil, nil, err
	}
	return toSet(fileList), toSet(dirList), nil
}

// persistLineage records the paths BOTH sides verifiably had at the end of
// a successful sync — strictly the intersection of the two manifests.
//
// Recording local-only paths (as this once did) is how a user's file got
// deleted: after a push-trigger sync we recorded "the peer has this file"
// before the peer had actually pulled it. The peer's pull kept failing (AV
// lock on a fresh .exe), so on the next run "in lineage but missing on
// peer" was misread as "the peer deleted it" — and the local original was
// removed. Unconfirmed pushes must never enter the lineage; they join it
// on the first sync after the peer's manifest actually contains them.
func (e *Engine) persistLineage(gameID, peerID string, local, remote delta.Manifest) {
	files, dirs := IntersectLineage(local, remote)
	if err := e.Store.SetSyncState(gameID, peerID, files, dirs); err != nil {
		e.Log("warn", fmt.Sprintf("persist sync lineage failed: %v", err))
	}
}

// IntersectLineage returns the sorted file and dir paths present in both
// manifests — the only paths that may count as "synced on both sides".
func IntersectLineage(local, remote delta.Manifest) (files, dirs []string) {
	files = make([]string, 0, len(local.Files))
	for p := range local.Files {
		if _, ok := remote.Files[p]; ok {
			files = append(files, p)
		}
	}
	sort.Strings(files)

	remoteDirs := toSet(remote.Dirs)
	dirs = make([]string, 0, len(local.Dirs))
	for _, d := range local.Dirs {
		if _, ok := remoteDirs[d]; ok {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return files, dirs
}

// RefreshLineage re-fetches the peer's manifest and re-persists the shared
// lineage. Called when a peer reports it finished pulling from us: the
// files we pushed are now really on both sides, so they can safely enter
// the lineage (making future local deletions of them propagate instead of
// the file being pulled back).
func (e *Engine) RefreshLineage(ctx context.Context, gameID string, peer Peer) {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return
	}
	isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath)
	remoteData, err := e.Transport.FetchManifest(ctx, peer, gameID, ManifestQuery{
		Name: game.Name, SavePath: game.SavePath, IsFile: isFile,
		AppID: game.AppID, CoverURL: game.CoverURL,
	})
	if err != nil {
		return
	}
	local, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return
	}
	e.persistLineage(gameID, peer.ID, local, remoteData.Manifest)
}

func (e *Engine) registerConflict(gameID string, peer Peer, localManifest delta.Manifest, remoteData ManifestResponse) {
	localSnap := SnapshotInfo{ID: "current", Timestamp: time.UnixMilli(int64(localManifest.LatestMtime)).UTC().Format(time.RFC3339), Comment: "Current active saves"}
	if latest, err := e.Snapshots.LatestSnapshot(gameID, ""); err == nil {
		localSnap = SnapshotInfo{ID: latest.ID, Timestamp: latest.Timestamp, Comment: latest.Comment}
	}
	remoteSnap := SnapshotInfo{ID: "remote-current", Timestamp: time.UnixMilli(int64(remoteData.Manifest.LatestMtime)).UTC().Format(time.RFC3339), Comment: "Current peer saves"}
	if remoteData.LatestSnapshot != nil {
		remoteSnap = *remoteData.LatestSnapshot
	}

	// Capture comparison data while we hold both manifests, so the UI can
	// show which side is further along and exactly what differs.
	diffs := diffManifests(localManifest, remoteData.Manifest)
	const maxDiffFiles = 100
	total := len(diffs)
	if len(diffs) > maxDiffFiles {
		diffs = diffs[:maxDiffFiles]
	}

	e.mu.Lock()
	e.activeConflicts[gameID] = &Conflict{
		Peer: peer, LocalSnap: localSnap, RemoteSnap: remoteSnap,
		LocalStats:  manifestStats(localManifest),
		RemoteStats: manifestStats(remoteData.Manifest),
		DiffFiles:   diffs,
		DiffTotal:   total,
	}
	e.mu.Unlock()

	e.Log("warn", fmt.Sprintf("sync conflict on %q with %q: both sides modified since last sync", gameID, peer.Name))
	if e.Progress.OnConflict != nil {
		e.Progress.OnConflict(gameID)
	}
}

// manifestStats summarises a manifest for the conflict comparison UI.
func manifestStats(m delta.Manifest) SideStats {
	s := SideStats{Files: len(m.Files), LatestMtimeMs: int64(m.LatestMtime)}
	for _, f := range m.Files {
		s.TotalBytes += f.Size
	}
	return s
}

// diffManifests lists every path that differs between the two sides,
// sorted by path.
func diffManifests(local, remote delta.Manifest) []DiffFile {
	var out []DiffFile
	for p, lf := range local.Files {
		if rf, ok := remote.Files[p]; ok {
			if lf.Hash != rf.Hash {
				out = append(out, DiffFile{Path: p, Status: "changed", LocalSize: lf.Size, RemoteSize: rf.Size})
			}
		} else {
			out = append(out, DiffFile{Path: p, Status: "only-local", LocalSize: lf.Size, RemoteSize: -1})
		}
	}
	for p, rf := range remote.Files {
		if _, ok := local.Files[p]; !ok {
			out = append(out, DiffFile{Path: p, Status: "only-remote", LocalSize: -1, RemoteSize: rf.Size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (e *Engine) applyLocalDeletions(game store.Game, d Decision) {
	for _, relPath := range d.FilesToDeleteLocally {
		if !delta.IsSafePath(game.SavePath, relPath) {
			e.Log("warn", "path traversal deletion denied: "+relPath)
			continue
		}
		full := filepath.Join(game.SavePath, filepath.FromSlash(relPath))
		_ = os.Chmod(full, 0o666)
		if err := os.Remove(full); err == nil {
			e.Log("info", "deleted locally (peer deleted): "+relPath)
		}
	}

	// Deepest first so children go before parents.
	dirs := append([]string{}, d.DirsToDeleteLocally...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, relDir := range dirs {
		if !delta.IsSafePath(game.SavePath, relDir) {
			continue
		}
		full := filepath.Join(game.SavePath, filepath.FromSlash(relDir))
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			if err := os.Remove(full); err == nil { // only removes empty dirs, matching rmdirSync
				e.Log("info", "deleted directory locally (peer deleted): "+relDir)
			}
		}
	}
}

func (e *Engine) propagateDeletions(ctx context.Context, peer Peer, gameID string, game store.Game, d Decision) {
	for _, relPath := range d.FilesToDeleteOnPeer {
		if !delta.IsSafePath(game.SavePath, relPath) {
			continue
		}
		if err := e.Transport.DeleteRemote(ctx, peer, gameID, relPath); err != nil {
			e.Log("warn", fmt.Sprintf("could not propagate deletion of %s: %v", relPath, err))
		}
	}
	dirs := append([]string{}, d.DirsToDeleteOnPeer...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, relDir := range dirs {
		if !delta.IsSafePath(game.SavePath, relDir) {
			continue
		}
		if err := e.Transport.DeleteRemote(ctx, peer, gameID, relDir); err != nil {
			e.Log("warn", fmt.Sprintf("could not propagate dir deletion of %s: %v", relDir, err))
		}
	}
}

func (e *Engine) createPulledDirs(game store.Game, dirsToPull []string) {
	dirs := append([]string{}, dirsToPull...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) < len(dirs[j]) }) // parents first
	for _, relDir := range dirs {
		if !delta.IsSafePath(game.SavePath, relDir) {
			continue
		}
		_ = os.MkdirAll(filepath.Join(game.SavePath, filepath.FromSlash(relDir)), 0o777)
	}
}

// pullFiles downloads and patches every file in filesToPull with bounded
// concurrency, progress reporting, throttling, and a mirror snapshot at
// the end.
func (e *Engine) pullFiles(ctx context.Context, peer Peer, gameID string, game store.Game,
	localManifest delta.Manifest, remoteData ManifestResponse, filesToPull []string) (retErr error) {

	deviceName := e.deviceName()
	if e.Progress.OnSyncStart != nil {
		e.Progress.OnSyncStart(gameID, ProgressEvent{PeerName: peer.Name, Direction: "download"})
	}
	e.Transport.ReportSyncEvent(peer, gameID, "sync-start", map[string]any{"peerName": deviceName, "direction": "upload"})

	defer func() {
		if retErr != nil {
			e.Transport.ReportSyncEvent(peer, gameID, "sync-error", map[string]any{
				"peerName": deviceName, "error": retErr.Error(), "direction": "upload",
			})
			if e.Progress.OnSyncError != nil {
				e.Progress.OnSyncError(gameID, ProgressEvent{PeerName: peer.Name, Error: retErr.Error()})
			}
		}
	}()

	// Make sure every remote directory exists before patching into it.
	for _, dir := range remoteData.Manifest.Dirs {
		if delta.IsSafePath(game.SavePath, dir) {
			_ = os.MkdirAll(filepath.Join(game.SavePath, filepath.FromSlash(dir)), 0o777)
		}
	}

	// Pre-compute per-file changed blocks, the total transfer byte count,
	// and the disk footprint (net growth + largest single new file, since
	// PatchFile writes a temp copy before renaming over the old version).
	changedBlocks := map[string][]int{}
	var totalBytes, netGrowth, maxNewSize int64
	for _, relPath := range filesToPull {
		remoteFile := remoteData.Manifest.Files[relPath]
		var localFile *delta.FileEntry
		var localSize int64
		if lf, ok := localManifest.Files[relPath]; ok {
			localFile = &lf
			localSize = lf.Size
		}
		netGrowth += remoteFile.Size - localSize
		if remoteFile.Size > maxNewSize {
			maxNewSize = remoteFile.Size
		}
		indices := DifferentBlockIndices(localFile, remoteFile)
		changedBlocks[relPath] = indices
		for _, idx := range indices {
			if idx < len(remoteFile.Blocks) {
				totalBytes += int64(remoteFile.Blocks[idx].Length)
			}
		}
	}

	// Fail early with a clear message if the drive can't hold the incoming
	// files, instead of crashing mid-write with a raw OS "disk full" error.
	if netGrowth < 0 {
		netGrowth = 0
	}
	const diskMargin = 16 << 20 // 16 MiB headroom
	needed := netGrowth + maxNewSize + diskMargin
	spaceDir := game.SavePath
	if fi, err := os.Stat(spaceDir); err != nil || !fi.IsDir() {
		spaceDir = filepath.Dir(game.SavePath)
	}
	if avail, ok := availableDiskBytes(spaceDir); ok && uint64(needed) > avail {
		return fmt.Errorf("not enough free storage: this sync needs about %s but only %s is free on the drive holding your save",
			humanBytes(needed), humanBytes(int64(avail)))
	}

	tracker := newProgressTracker(totalBytes)
	throttle := e.throttleFor(peer.Wan())

	// Progress reporter shared by the per-file loop and the block-group
	// loop inside each file. Without in-file reporting, a single large
	// file (e.g. an 18MB save pulled over the relay) sat at 0% for its
	// whole transfer. Throttled so relay round-trips don't spam the UI.
	var lastReport time.Time
	reportProgress := func(force bool) {
		if !force && time.Since(lastReport) < 500*time.Millisecond {
			return
		}
		lastReport = time.Now()
		bytesPulled, speed, pct := tracker.stats()
		ev := ProgressEvent{PeerName: peer.Name, BytesTransferred: bytesPulled, TotalBytes: totalBytes, SpeedBytesPerSec: speed, Percentage: pct}
		if e.Progress.OnSyncProgress != nil {
			e.Progress.OnSyncProgress(gameID, ev)
		}
		e.Transport.ReportSyncEvent(peer, gameID, "sync-progress", map[string]any{
			"peerName": deviceName, "bytesTransferred": bytesPulled, "totalBytes": totalBytes,
			"speedBytesPerSec": speed, "percentage": pct,
		})
	}

	for _, relPath := range filesToPull {
		if !delta.IsSafePath(game.SavePath, relPath) {
			return fmt.Errorf("path traversal attempt on pulled file %s", relPath)
		}
		remoteFile := remoteData.Manifest.Files[relPath]
		indices := changedBlocks[relPath]

		blocks, err := e.fetchFileBlocks(ctx, peer, gameID, relPath, remoteFile, indices, throttle, tracker, reportProgress)
		if err != nil {
			return err
		}

		localFilePath := filepath.Join(game.SavePath, filepath.FromSlash(relPath))
		if isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath); isFile {
			localFilePath = game.SavePath // single-file save mode
		}
		if err := delta.PatchFile(localFilePath, remoteFile, blocks); err != nil {
			return fmt.Errorf("patch %s: %w", relPath, err)
		}
		if remoteFile.MtimeMs > 0 {
			mtime := time.UnixMilli(int64(remoteFile.MtimeMs))
			_ = os.Chtimes(localFilePath, mtime, mtime)
		}
		e.Log("info", "file updated: "+relPath)

		// File-boundary progress reporting (always fires).
		reportProgress(true)
	}

	// Mirror the peer's latest snapshot locally so both sides share history.
	if remoteData.LatestSnapshot != nil {
		e.recordMirrorSnapshot(gameID, game, peer, *remoteData.LatestSnapshot,
			fmt.Sprintf("Synced from peer: %s (%s)", peer.Name, remoteData.LatestSnapshot.Comment))
	}

	e.Transport.ReportSyncEvent(peer, gameID, "sync-complete", map[string]any{"peerName": deviceName, "direction": "upload"})
	if e.Progress.OnSyncComplete != nil {
		e.Progress.OnSyncComplete(gameID, ProgressEvent{PeerName: peer.Name, Direction: "download"})
	}
	return nil
}

// fetchFileBlocks pulls one file's changed blocks in concurrent batch
// groups (3 WAN / 5 LAN at a time, group-boundary waits) with per-batch
// retries.
func (e *Engine) fetchFileBlocks(ctx context.Context, peer Peer, gameID, relPath string,
	remoteFile delta.FileEntry, indices []int, throttle *throttler, tracker *progressTracker,
	onGroup func(force bool)) ([]delta.BlockSource, error) {

	batches := BatchIndices(indices, remoteFile.BlockSize, peer.Wan())
	concurrency := ConcurrencyFor(peer.Wan())

	var all []delta.BlockSource
	for groupStart := 0; groupStart < len(batches); groupStart += concurrency {
		groupEnd := groupStart + concurrency
		if groupEnd > len(batches) {
			groupEnd = len(batches)
		}
		group := batches[groupStart:groupEnd]

		results := make([][]BlockData, len(group))
		errs := make([]error, len(group))
		var wg sync.WaitGroup
		for i, batch := range group {
			wg.Add(1)
			go func(i int, batch []int) {
				defer wg.Done()
				results[i], errs[i] = fetchWithRetry(ctx, e.Transport, peer, gameID, relPath, batch, remoteFile.BlockSize, e.Log)
			}(i, batch)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				return nil, fmt.Errorf("fetch blocks for %s: %w", relPath, err)
			}
			var groupBytes int64
			for _, b := range results[i] {
				all = append(all, delta.BlockSource{Index: b.Index, Data: b.Data})
				groupBytes += int64(b.Length)
			}
			tracker.add(groupBytes)
			if onGroup != nil {
				onGroup(false) // in-file progress so big files don't sit at 0%
			}
			throttle.wait(ctx, groupBytes)
		}
	}
	return all, nil
}

func fetchWithRetry(ctx context.Context, t Transport, peer Peer, gameID, relPath string,
	indices []int, blockSize int, logf func(string, string)) ([]BlockData, error) {

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		blocks, err := t.FetchBlocks(ctx, peer, gameID, relPath, indices, blockSize)
		if err == nil {
			return blocks, nil
		}
		lastErr = err
		logf("warn", fmt.Sprintf("block fetch attempt %d/%d failed for %s: %v", attempt, maxAttempts, relPath, err))
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second): // linear backoff
			}
		}
	}
	return nil, lastErr
}

// recordMirrorSnapshot zips the (just-updated) local save under the peer's
// snapshot id so both devices show the same history entry.
func (e *Engine) recordMirrorSnapshot(gameID string, game store.Game, peer Peer, remoteSnap SnapshotInfo, comment string) {
	if _, err := e.Store.GetSnapshot(remoteSnap.ID); err == nil {
		return // already mirrored
	}

	settings, err := e.Store.GetSettings()
	if err != nil {
		return
	}
	backupsDir := settings.SyncBackupsDir
	if backupsDir == "" {
		backupsDir = settings.BackupsDir
	}
	destDir := filepath.Join(backupsDir, gameID, game.ActiveBranch)
	if err := os.MkdirAll(destDir, 0o777); err != nil {
		return
	}
	zipPath := filepath.Join(destDir, remoteSnap.ID+".zip")
	if err := snapshot.ZipPath(game.SavePath, zipPath); err != nil {
		e.Log("warn", fmt.Sprintf("mirror snapshot zip failed: %v", err))
		return
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		return
	}

	_ = e.Store.CreateSnapshot(store.Snapshot{
		ID:           remoteSnap.ID,
		GameID:       gameID,
		BranchName:   game.ActiveBranch,
		Timestamp:    remoteSnap.Timestamp,
		Comment:      comment,
		IsSystemAuto: true,
		ZipPath:      zipPath,
		SizeBytes:    info.Size(),
	})
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (e *Engine) deviceName() string {
	settings, err := e.Store.GetSettings()
	if err != nil {
		return "OpenSave"
	}
	return settings.DeviceName
}

// throttler enforces the WAN speed limit by pausing after each batch group
// proportionally to the bytes just transferred (delay = bytes / limit).
type throttler struct {
	limitBytesPerSec int64
}

func (e *Engine) throttleFor(isWan bool) *throttler {
	if !isWan {
		return &throttler{}
	}
	settings, err := e.Store.GetSettings()
	if err != nil || settings.SpeedLimitKbps <= 0 {
		return &throttler{}
	}
	return &throttler{limitBytesPerSec: int64(settings.SpeedLimitKbps) * 1024}
}

func (t *throttler) wait(ctx context.Context, bytes int64) {
	if t.limitBytesPerSec <= 0 || bytes <= 0 {
		return
	}
	delay := time.Duration(bytes*int64(time.Second)/t.limitBytesPerSec)
	if delay < 50*time.Millisecond {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
}

// progressTracker accumulates transferred bytes and derives speed/percent.
type progressTracker struct {
	mu         sync.Mutex
	start      time.Time
	total      int64
	transferred int64
}

func newProgressTracker(total int64) *progressTracker {
	return &progressTracker{start: time.Now(), total: total}
}

func (p *progressTracker) add(bytes int64) {
	p.mu.Lock()
	p.transferred += bytes
	p.mu.Unlock()
}

func (p *progressTracker) stats() (transferred int64, speedBytesPerSec float64, percentage int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := time.Since(p.start).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(p.transferred) / elapsed
	}
	pct := 100
	if p.total > 0 {
		pct = int(p.transferred * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
	}
	return p.transferred, speed, pct
}
