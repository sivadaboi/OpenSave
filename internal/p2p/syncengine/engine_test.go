package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// fakeTransport serves the sync protocol from a real directory on disk,
// acting as the "remote peer" without any networking.
type fakeTransport struct {
	mu           sync.Mutex
	remoteDir    string
	remoteBranch string
	latestSnap   *SnapshotInfo

	deletedOnPeer []string
	pullTriggers  int
	syncEvents    []string
}

func (f *fakeTransport) FetchManifest(ctx context.Context, peer Peer, gameID string, q ManifestQuery) (ManifestResponse, error) {
	m, err := delta.BuildManifest(f.remoteDir)
	if err != nil {
		return ManifestResponse{}, err
	}
	branch := f.remoteBranch
	if branch == "" {
		branch = "main"
	}
	return ManifestResponse{Manifest: m, ActiveBranch: branch, LatestSnapshot: f.latestSnap}, nil
}

func (f *fakeTransport) FetchBlocks(ctx context.Context, peer Peer, gameID, relPath string, blockIndices []int, blockSize int) ([]BlockData, error) {
	fullPath := filepath.Join(f.remoteDir, filepath.FromSlash(relPath))
	entry, err := delta.HashFile(fullPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}

	var out []BlockData
	for _, idx := range blockIndices {
		if idx >= len(entry.Blocks) {
			continue
		}
		start := idx * entry.BlockSize
		end := start + entry.Blocks[idx].Length
		if end > len(raw) {
			end = len(raw)
		}
		out = append(out, BlockData{Index: idx, Data: raw[start:end], Length: end - start})
	}
	return out, nil
}

func (f *fakeTransport) DeleteRemote(ctx context.Context, peer Peer, gameID, relPath string) error {
	f.mu.Lock()
	f.deletedOnPeer = append(f.deletedOnPeer, relPath)
	f.mu.Unlock()
	full := filepath.Join(f.remoteDir, filepath.FromSlash(relPath))
	return os.RemoveAll(full)
}

func (f *fakeTransport) TriggerPeerPull(peer Peer, gameID string) {
	f.mu.Lock()
	f.pullTriggers++
	f.mu.Unlock()
}

func (f *fakeTransport) ReportSyncEvent(peer Peer, gameID, eventType string, data map[string]any) {
	f.mu.Lock()
	f.syncEvents = append(f.syncEvents, eventType)
	f.mu.Unlock()
}

type engineEnv struct {
	engine    *Engine
	store     *store.Store
	transport *fakeTransport
	localDir  string
	remoteDir string
	peer      Peer
}

func setupEngine(t *testing.T) *engineEnv {
	t.Helper()
	root := t.TempDir()
	localDir := filepath.Join(root, "local-saves")
	remoteDir := filepath.Join(root, "remote-saves")
	for _, d := range []string{localDir, remoteDir} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}

	s, err := store.Open(filepath.Join(root, "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(root, filepath.Join(root, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(store.Game{ID: "game1", Name: "Game One", SavePath: localDir, MaxSnapshots: 10}); err != nil {
		t.Fatal(err)
	}

	peer := Peer{ID: "node_remote", Name: "Remote Device", Address: "127.0.0.1", Port: 9999}
	if err := s.UpsertPeer(store.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port, Status: "online"}); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{remoteDir: remoteDir}
	eng := New(s, snapshot.New(s), transport)
	return &engineEnv{engine: eng, store: s, transport: transport, localDir: localDir, remoteDir: remoteDir, peer: peer}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func TestSync_PullNewRemoteFile(t *testing.T) {
	env := setupEngine(t)
	write(t, env.remoteDir, "save.dat", "remote progress")
	env.transport.latestSnap = &SnapshotInfo{ID: "snap_777", Timestamp: "2026-06-01T00:00:00.000Z", Comment: "peer snap"}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("SyncWithPeer error = %v", err)
	}
	if res.Status != "updated" || res.Direction != "pull" {
		t.Errorf("result = %+v, want updated/pull", res)
	}

	got, err := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "remote progress" {
		t.Errorf("pulled content = %q", got)
	}

	// The peer's snapshot must be mirrored locally.
	if _, err := env.store.GetSnapshot("snap_777"); err != nil {
		t.Errorf("mirror snapshot not recorded: %v", err)
	}

	// Sync events must have flowed to the peer.
	if len(env.transport.syncEvents) == 0 || env.transport.syncEvents[0] != "sync-start" {
		t.Errorf("expected sync-start event first, got %v", env.transport.syncEvents)
	}
	last := env.transport.syncEvents[len(env.transport.syncEvents)-1]
	if last != "sync-complete" {
		t.Errorf("expected sync-complete last, got %v", env.transport.syncEvents)
	}
}

func TestSync_InsufficientDiskSpace(t *testing.T) {
	env := setupEngine(t)
	write(t, env.remoteDir, "save.dat", "remote progress that needs space")

	// Simulate a nearly-full destination drive.
	orig := availableDiskBytes
	availableDiskBytes = func(string) (uint64, bool) { return 10, true }
	defer func() { availableDiskBytes = orig }()

	_, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err == nil || !strings.Contains(err.Error(), "not enough free storage") {
		t.Fatalf("expected a free-storage error, got %v", err)
	}

	// The pull must abort before writing anything.
	if _, statErr := os.Stat(filepath.Join(env.localDir, "save.dat")); statErr == nil {
		t.Error("save.dat should not exist when the disk-space check fails")
	}
}

func TestSync_PushTriggersPeerPull(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "local only")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "triggered_peer_pull" || res.Direction != "push" {
		t.Errorf("result = %+v, want triggered_peer_pull/push", res)
	}
	if env.transport.pullTriggers != 1 {
		t.Errorf("pull triggers = %d, want 1", env.transport.pullTriggers)
	}
}

func TestSync_DeletionPropagation(t *testing.T) {
	env := setupEngine(t)

	// Establish shared lineage: both sides had a.dat and b.dat, written an
	// hour ago, last synced 30 minutes ago (files unchanged since).
	write(t, env.localDir, "a.dat", "shared-a")
	write(t, env.localDir, "b.dat", "shared-b")
	write(t, env.remoteDir, "a.dat", "shared-a")
	write(t, env.remoteDir, "b.dat", "shared-b")
	hourAgo := time.Now().Add(-1 * time.Hour)
	for _, dir := range []string{env.localDir, env.remoteDir} {
		for _, f := range []string{"a.dat", "b.dat"} {
			if err := os.Chtimes(filepath.Join(dir, f), hourAgo, hourAgo); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"a.dat", "b.dat"}, nil); err != nil {
		t.Fatal(err)
	}
	lastSync := time.Now().Add(-30 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, lastSync); err != nil {
		t.Fatal(err)
	}

	// We delete a.dat locally; the peer deleted b.dat on their side.
	if err := os.Remove(filepath.Join(env.localDir, "a.dat")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(env.remoteDir, "b.dat")); err != nil {
		t.Fatal(err)
	}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "deletions_synced" {
		t.Errorf("result = %+v, want deletions_synced", res)
	}

	// Our deletion propagated to the peer.
	if len(env.transport.deletedOnPeer) != 1 || env.transport.deletedOnPeer[0] != "a.dat" {
		t.Errorf("deletedOnPeer = %v, want [a.dat]", env.transport.deletedOnPeer)
	}
	// The peer's deletion applied locally.
	if _, err := os.Stat(filepath.Join(env.localDir, "b.dat")); !os.IsNotExist(err) {
		t.Error("b.dat should have been deleted locally (peer deleted it)")
	}
}

func TestSync_ConflictDetectedAndResolvedKeepRemote(t *testing.T) {
	env := setupEngine(t)

	// Both sides changed the same file since the last sync.
	write(t, env.localDir, "save.dat", "local version")
	write(t, env.remoteDir, "save.dat", "remote version")
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"save.dat"}, nil); err != nil {
		t.Fatal(err)
	}
	// Last sync long ago; both mtimes are "now" — конflict condition.
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Fatalf("result = %+v, want conflict", res)
	}
	if len(env.engine.ActiveConflicts()) != 1 {
		t.Fatal("expected one active conflict")
	}

	// A second sync attempt while unresolved must short-circuit.
	res2, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "conflict" {
		t.Errorf("second sync should still report conflict, got %+v", res2)
	}
	local, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "local version" {
		t.Error("local save must be untouched while a conflict is pending")
	}

	// Resolve: keep-remote overwrites local.
	if _, err := env.engine.ResolveConflict(context.Background(), "game1", env.peer.ID, "keep-remote"); err != nil {
		t.Fatalf("ResolveConflict error = %v", err)
	}
	local, _ = os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "remote version" {
		t.Errorf("after keep-remote, local = %q, want remote version", local)
	}
	if len(env.engine.ActiveConflicts()) != 0 {
		t.Error("conflict should be cleared after resolution")
	}
}

// TestConflict_CarriesComparisonData verifies the conflict captures per-side
// stats and the differing file list, and that keep-remote snapshots the
// local version first so the choice is undoable.
func TestConflict_CarriesComparisonData(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "local version")
	write(t, env.localDir, "only-here.cfg", "mine")
	write(t, env.remoteDir, "save.dat", "remote version longer")
	write(t, env.remoteDir, "only-there.cfg", "theirs")
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"save.dat"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}

	if res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil || res.Status != "conflict" {
		t.Fatalf("expected conflict, got %+v err=%v", res, err)
	}
	c, ok := env.engine.ActiveConflicts()["game1"]
	if !ok {
		t.Fatal("no active conflict")
	}

	if c.LocalStats.Files != 2 || c.RemoteStats.Files != 2 {
		t.Errorf("stats files = %d/%d, want 2/2", c.LocalStats.Files, c.RemoteStats.Files)
	}
	if c.LocalStats.TotalBytes == 0 || c.RemoteStats.TotalBytes == 0 {
		t.Error("stats total bytes should be non-zero")
	}
	if c.DiffTotal != 3 || len(c.DiffFiles) != 3 {
		t.Fatalf("diff count = %d (%d listed), want 3", c.DiffTotal, len(c.DiffFiles))
	}
	byPath := map[string]DiffFile{}
	for _, d := range c.DiffFiles {
		byPath[d.Path] = d
	}
	if byPath["save.dat"].Status != "changed" {
		t.Errorf("save.dat status = %q, want changed", byPath["save.dat"].Status)
	}
	if byPath["only-here.cfg"].Status != "only-local" || byPath["only-here.cfg"].RemoteSize != -1 {
		t.Errorf("only-here.cfg = %+v, want only-local with RemoteSize -1", byPath["only-here.cfg"])
	}
	if byPath["only-there.cfg"].Status != "only-remote" || byPath["only-there.cfg"].LocalSize != -1 {
		t.Errorf("only-there.cfg = %+v, want only-remote with LocalSize -1", byPath["only-there.cfg"])
	}

	// keep-remote must snapshot the local version before overwriting.
	if _, err := env.engine.ResolveConflict(context.Background(), "game1", env.peer.ID, "keep-remote"); err != nil {
		t.Fatal(err)
	}
	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range snaps {
		if strings.Contains(s.Comment, "before keeping") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a safety snapshot of the local version; snapshots: %+v", snaps)
	}
	got, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(got) != "remote version longer" {
		t.Errorf("local after keep-remote = %q", got)
	}
}

func TestSync_ConflictResolvedKeepLocal(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "local version")
	write(t, env.remoteDir, "save.dat", "remote version")
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}

	if res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil || res.Status != "conflict" {
		t.Fatalf("expected conflict, got %+v err=%v", res, err)
	}

	if _, err := env.engine.ResolveConflict(context.Background(), "game1", env.peer.ID, "keep-local"); err != nil {
		t.Fatal(err)
	}
	// keep-local is purely local: our files are untouched AND we must NOT
	// force the peer to overwrite its save (consent — the peer decides for
	// itself).
	if env.transport.pullTriggers != 0 {
		t.Errorf("keep-local must not force the peer; pull triggers = %d, want 0", env.transport.pullTriggers)
	}
	local, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "local version" {
		t.Error("keep-local must not modify local files")
	}
	if len(env.engine.ActiveConflicts()) != 0 {
		t.Error("conflict should be cleared after keep-local")
	}
}

func TestSync_ConflictResolvedMergeBranch(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "local version")
	write(t, env.remoteDir, "save.dat", "remote version")
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}

	if res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil || res.Status != "conflict" {
		t.Fatalf("expected conflict, got %+v err=%v", res, err)
	}

	branchName, err := env.engine.ResolveConflict(context.Background(), "game1", env.peer.ID, "merge-branch")
	if err != nil {
		t.Fatalf("ResolveConflict error = %v", err)
	}
	if branchName == "" {
		t.Fatal("merge-branch should return the new branch name")
	}

	// Active branch is now the conflict branch holding the REMOTE state.
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	if game.ActiveBranch != branchName {
		t.Errorf("active branch = %q, want %q", game.ActiveBranch, branchName)
	}
	local, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "remote version" {
		t.Errorf("conflict branch should hold remote state, got %q", local)
	}

	// The LOCAL version must be recoverable: switching back to main
	// restores the pre-switch auto-snapshot of the local state.
	if err := env.engine.Snapshots.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	local, _ = os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "local version" {
		t.Errorf("main branch should restore local version, got %q", local)
	}
}

func TestSync_InSyncRefreshesLineage(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "identical")
	write(t, env.remoteDir, "save.dat", "identical")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "in_sync" {
		t.Errorf("result = %+v, want in_sync", res)
	}
	files, _, err := env.store.GetSyncState("game1", env.peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "save.dat" {
		t.Errorf("lineage should refresh on in_sync, got %v", files)
	}
}

func TestSync_ModifiedFileNewerRemoteWins(t *testing.T) {
	env := setupEngine(t)

	write(t, env.localDir, "save.dat", "old local")
	// Make local mtime clearly older.
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(env.localDir, "save.dat"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	write(t, env.remoteDir, "save.dat", "newer remote content!")

	// Lineage includes the file and last sync was after the local edit —
	// only remote modified -> plain pull, no conflict.
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"save.dat"}, nil); err != nil {
		t.Fatal(err)
	}
	lastSync := time.Now().Add(-30 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, lastSync); err != nil {
		t.Fatal(err)
	}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "updated" {
		t.Fatalf("result = %+v, want updated (pull)", res)
	}
	local, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(local) != "newer remote content!" {
		t.Errorf("local = %q, want newer remote content", local)
	}
}
