package snapshot

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
)

type testEnv struct {
	mgr      *Manager
	store    *store.Store
	saveDir  string
	backups  string
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "opensave.db")
	saveDir := filepath.Join(root, "saves", "game1")
	backups := filepath.Join(root, "backups")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if err := s.EnsureDefaultSettings(root, backups); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(saveDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(store.Game{ID: "game1", Name: "Game One", SavePath: saveDir, MaxSnapshots: 3}); err != nil {
		t.Fatal(err)
	}

	mgr := New(s)
	// Monotonic fake clock so snapshot IDs (snap_<ms>) never collide even
	// when tests create several within the same millisecond.
	base := time.Now()
	tick := 0
	mgr.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	return &testEnv{mgr: mgr, store: s, saveDir: saveDir, backups: backups}
}

func writeSave(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndRestoreRoundTrip(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "checkpoint alpha")
	writeSave(t, env.saveDir, "config/settings.ini", "vsync=1")

	snap, err := env.mgr.Create("game1", "before boss", false)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if snap.Comment != "before boss" || snap.IsSystemAuto {
		t.Errorf("snapshot metadata wrong: %+v", snap)
	}
	if _, err := os.Stat(snap.ZipPath); err != nil {
		t.Fatalf("zip file missing: %v", err)
	}

	// Wreck the save, then restore.
	writeSave(t, env.saveDir, "slot1.sav", "corrupted!!!")
	if err := os.RemoveAll(filepath.Join(env.saveDir, "config")); err != nil {
		t.Fatal(err)
	}

	if _, err := env.mgr.Restore("game1", snap.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "checkpoint alpha" {
		t.Errorf("restored content = %q, want %q", got, "checkpoint alpha")
	}
	if _, err := os.Stat(filepath.Join(env.saveDir, "config", "settings.ini")); err != nil {
		t.Errorf("nested file not restored: %v", err)
	}
}

func TestRestore_TakesSafetySnapshotFirst(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "original")
	snap, err := env.mgr.Create("game1", "", false)
	if err != nil {
		t.Fatal(err)
	}

	writeSave(t, env.saveDir, "slot1.sav", "newer unsaved progress")
	if _, err := env.mgr.Restore("game1", snap.ID); err != nil {
		t.Fatal(err)
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	// Original + safety snapshot of the "newer unsaved progress" state.
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots (original + safety), got %d", len(snaps))
	}
	if !snaps[0].IsSystemAuto {
		t.Error("newest snapshot should be the auto safety snapshot")
	}
}

func TestRetentionPruning(t *testing.T) {
	env := setup(t) // maxSnapshots = 3
	writeSave(t, env.saveDir, "slot1.sav", "v0")

	var zipPaths []string
	for i := 0; i < 5; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "v"+string(rune('0'+i)))
		snap, err := env.mgr.Create("game1", "", true)
		if err != nil {
			t.Fatal(err)
		}
		zipPaths = append(zipPaths, snap.ZipPath)
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Errorf("expected 3 snapshots after pruning, got %d", len(snaps))
	}
	// The two oldest zip files must be gone from disk.
	for _, pruned := range zipPaths[:2] {
		if _, err := os.Stat(pruned); !os.IsNotExist(err) {
			t.Errorf("pruned zip %s should be deleted", pruned)
		}
	}
	for _, kept := range zipPaths[2:] {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("retained zip %s should still exist: %v", kept, err)
		}
	}
}

// TestRestoreOldestAtRetentionLimit guards a data-loss bug: restoring the
// oldest snapshot while the game is at its retention limit used to fail,
// because the safety snapshot taken first pushed the target beyond the
// limit and pruning deleted its archive before extraction.
func TestRestoreOldestAtRetentionLimit(t *testing.T) {
	env := setup(t) // maxSnapshots = 3

	// Fill exactly to the limit; snap0 is the oldest and holds "v0".
	var snaps []store.Snapshot
	for i := 0; i < 3; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "v"+string(rune('0'+i)))
		s, err := env.mgr.Create("game1", "", false)
		if err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, s)
	}
	oldest := snaps[0]

	// Change the save so a safety snapshot is taken (which triggers pruning).
	writeSave(t, env.saveDir, "slot1.sav", "current")

	if _, err := env.mgr.Restore("game1", oldest.ID); err != nil {
		t.Fatalf("Restore of oldest snapshot at retention limit failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v0" {
		t.Errorf("restored content = %q, want %q", got, "v0")
	}
}

func TestBranchSwitchRoundTrip(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "main branch save")

	cleanName, err := env.mgr.CreateBranch("game1", "NG+ Run!")
	if err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}
	if cleanName != "ngrun" {
		t.Errorf("branch name sanitization: got %q, want %q", cleanName, "ngrun")
	}

	if err := env.mgr.SwitchBranch("game1", cleanName); err != nil {
		t.Fatalf("SwitchBranch() error = %v", err)
	}

	// New branch has no snapshots -> save dir should be cleared.
	entries, err := os.ReadDir(env.saveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("save dir should be empty on a fresh branch, has %d entries", len(entries))
	}

	// Write NG+ progress, snapshot lands on the new branch.
	writeSave(t, env.saveDir, "slot1.sav", "ng+ save")
	if _, err := env.mgr.Create("game1", "", true); err != nil {
		t.Fatal(err)
	}

	// Switch back to main: the pre-switch auto-snapshot of main must restore.
	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatalf("switch back error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "main branch save" {
		t.Errorf("after switching back to main, save = %q, want %q", got, "main branch save")
	}

	// And forward again to the NG+ branch.
	if err := env.mgr.SwitchBranch("game1", cleanName); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(env.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ng+ save" {
		t.Errorf("after switching to %s, save = %q, want %q", cleanName, got, "ng+ save")
	}
}

func TestSingleFileSaveMode(t *testing.T) {
	env := setup(t)
	// Re-point the game at a single file instead of a directory.
	saveFile := filepath.Join(filepath.Dir(env.saveDir), "profile.sav")
	if err := os.WriteFile(saveFile, []byte("single file save"), 0o666); err != nil {
		t.Fatal(err)
	}
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	game.SavePath = saveFile
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	snap, err := env.mgr.Create("game1", "", false)
	if err != nil {
		t.Fatalf("Create() single-file error = %v", err)
	}

	if err := os.WriteFile(saveFile, []byte("overwritten"), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := env.mgr.Restore("game1", snap.ID); err != nil {
		t.Fatalf("Restore() single-file error = %v", err)
	}
	got, err := os.ReadFile(saveFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "single file save" {
		t.Errorf("restored single-file save = %q, want %q", got, "single file save")
	}
}

func TestUploadHookFires(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "data")

	done := make(chan string, 1)
	env.mgr.OnUpload = func(zipPath, remoteFileName string) {
		done <- remoteFileName
	}

	snap, err := env.mgr.Create("game1", "", false)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case remoteName := <-done:
		want := "game1__main__" + snap.ID + ".zip"
		if remoteName != want {
			t.Errorf("remote filename = %q, want %q", remoteName, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload hook never fired")
	}
}

// TestEmptySnapshotNeverMirrorsToCloud: a snapshot of an empty save dir
// (usually a mis-tracked path) stays local with a loud warning and is
// never uploaded — field report was an "empty backup" sitting silently
// in a tester's WebDAV storage.
func TestEmptySnapshotNeverMirrorsToCloud(t *testing.T) {
	env := setup(t)

	// OnUpload fires on a background goroutine, so the counter it writes
	// must be synchronized against the test's reads (atomic).
	var uploads atomic.Int32
	env.mgr.OnUpload = func(zipPath, remoteName string) { uploads.Add(1) }
	var mu sync.Mutex
	var warned string
	env.mgr.Log = func(level, msg string) {
		if level == "warn" {
			mu.Lock()
			warned = msg
			mu.Unlock()
		}
	}

	// Save dir exists but holds nothing.
	snap, err := env.mgr.Create("game1", "", true)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(snap.ZipPath); err != nil {
		t.Fatalf("empty snapshot should still exist locally: %v", err)
	}
	if uploads.Load() != 0 {
		t.Errorf("empty snapshot was mirrored to cloud (%d uploads)", uploads.Load())
	}
	mu.Lock()
	gotWarn := warned
	mu.Unlock()
	if gotWarn == "" || !strings.Contains(gotWarn, "no files") {
		t.Errorf("expected a loud warning about the empty snapshot, got %q", gotWarn)
	}

	// With real content the mirror fires again.
	writeSave(t, env.saveDir, "slot1.sav", "actual progress")
	if _, err := env.mgr.Create("game1", "", true); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for uploads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if uploads.Load() != 1 {
		t.Errorf("non-empty snapshot should mirror exactly once, got %d", uploads.Load())
	}
}

// TestPruneAllBranches: retention must clean EVERY branch, not just the
// active one — conflict-* and manual branches otherwise pile up snapshots
// forever (a real disk-filler behind "my system is full of snapshots").
func TestPruneAllBranches(t *testing.T) {
	env := setup(t)
	// Limit 2 per branch.
	g, _ := env.store.GetGame("game1")
	g.MaxSnapshots = 2
	_ = env.store.UpdateGame(g)

	// main branch: 4 snapshots.
	for i := 0; i < 4; i++ {
		writeSave(t, env.saveDir, "slot.sav", string(rune('a'+i)))
		if _, err := env.mgr.Create("game1", "", false); err != nil {
			t.Fatal(err)
		}
	}
	// a side branch with its own snapshots (simulating a conflict branch).
	if _, err := env.mgr.CreateBranch("game1", "side"); err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", "side"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		writeSave(t, env.saveDir, "slot.sav", string(rune('m'+i)))
		if _, err := env.mgr.Create("game1", "", false); err != nil {
			t.Fatal(err)
		}
	}

	// The active branch (side) auto-pruned during Create; now prune all.
	removed, freed, err := env.mgr.PruneAllGames()
	if err != nil {
		t.Fatal(err)
	}
	_ = removed
	_ = freed

	for _, branch := range []string{"main", "side"} {
		snaps, err := env.store.ListSnapshots("game1", branch)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != 2 {
			t.Errorf("branch %q has %d snapshots after prune, want 2 (limit)", branch, len(snaps))
		}
		for _, s := range snaps {
			if _, err := os.Stat(s.ZipPath); err != nil {
				t.Errorf("kept snapshot %s has no zip: %v", s.ID, err)
			}
		}
	}
}

// TestCleanupSweepsAbandonedConflictBranches reproduces the real report:
// a game with many conflict-* branches, each UNDER the per-branch limit,
// so per-branch pruning finds nothing — yet the branches are junk from
// resolved conflicts and should be swept by the cleanup action.
func TestCleanupSweepsAbandonedConflictBranches(t *testing.T) {
	env := setup(t)
	g, _ := env.store.GetGame("game1")
	g.MaxSnapshots = 10
	_ = env.store.UpdateGame(g)

	writeSave(t, env.saveDir, "slot.sav", "main-state")
	if _, err := env.mgr.Create("game1", "", true); err != nil {
		t.Fatal(err)
	}
	// Three abandoned conflict branches, 2 snapshots each (all < limit 10).
	for _, b := range []string{"conflict-omar-1111", "conflict-omar-2222", "conflict-omar-3333"} {
		if _, err := env.mgr.CreateBranch("game1", b); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			env.store.CreateSnapshot(store.Snapshot{
				ID: b + "-snap" + string(rune('0'+i)), GameID: "game1", BranchName: b,
				Timestamp: "2026-01-01T00:00:00.000Z", ZipPath: filepath.Join(env.backups, "x.zip"),
			})
		}
	}

	removed, _, err := env.mgr.PruneAllGames()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 { // 3 branches * 2 snapshots
		t.Errorf("cleanup removed %d, want 6 (the abandoned conflict-branch snapshots)", removed)
	}
	// The active branch (main) and its snapshot must survive.
	remaining, _ := env.store.ListBranches("game1")
	for _, b := range remaining {
		if strings.HasPrefix(b, "conflict-") {
			t.Errorf("abandoned conflict branch %q was not swept", b)
		}
	}
	if snaps, _ := env.store.ListSnapshots("game1", "main"); len(snaps) != 1 {
		t.Errorf("main branch lost its snapshot: %d remain", len(snaps))
	}
}

// Snapshot ids are snap_<unix-millis>, so two taken in the same millisecond
// collided on the primary key and the second simply failed — a backup that
// silently did not happen, which is the worst way for this to fail.
//
// The harness above hides it with a monotonic fake clock, which is why this
// only ever appeared in e2e, on the real clock, as an intermittent
// "UNIQUE constraint failed: snapshots.id". Freezing the clock reproduces it
// on demand.
func TestCreateSnapshotsWithinOneMillisecond(t *testing.T) {
	env := setup(t)

	// A separate game with generous retention: game1 keeps only 3, and
	// pruning frees an id that the next snapshot may legitimately reuse,
	// which would measure retention rather than collision handling.
	if err := env.store.CreateGame(store.Game{
		ID: "burst", Name: "Burst", SavePath: env.saveDir, MaxSnapshots: 50,
	}); err != nil {
		t.Fatal(err)
	}

	// A clock that does not advance at all: every snapshot claims the same
	// millisecond, which is what a fast machine does anyway.
	frozen := time.Now()
	env.mgr.now = func() time.Time { return frozen }

	writeSave(t, env.saveDir, "slot1.sav", "one")

	ids := map[string]bool{}
	for i := 0; i < 5; i++ {
		snap, err := env.mgr.Create("burst", "same instant", false)
		if err != nil {
			t.Fatalf("snapshot %d failed: %v — two snapshots in one millisecond must both succeed", i+1, err)
		}
		if ids[snap.ID] {
			t.Fatalf("snapshot %d reused id %s", i+1, snap.ID)
		}
		ids[snap.ID] = true
	}

	if len(ids) != 5 {
		t.Errorf("got %d distinct ids from 5 snapshots", len(ids))
	}

	// The id must stay parseable as a timestamp: the app reads the time back
	// out of it, so a suffix or any non-numeric tail would break history.
	for id := range ids {
		msStr := strings.TrimPrefix(id, "snap_")
		if _, err := strconv.ParseInt(msStr, 10, 64); err != nil {
			t.Errorf("id %q is not snap_<millis> any more: %v", id, err)
		}
	}
}

// The sequential case above is only half of it. Choosing an id and inserting
// it are two steps, and between them another goroutine can claim the same id
// — which is what actually happened: syncs snapshot in background goroutines,
// so the collision survived a fix that only checked for a free id first.
func TestConcurrentSnapshotsInOneMillisecond(t *testing.T) {
	env := setup(t)
	if err := env.store.CreateGame(store.Game{
		ID: "race", Name: "Race", SavePath: env.saveDir, MaxSnapshots: 100,
	}); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot1.sav", "one")

	frozen := time.Now()
	env.mgr.now = func() time.Time { return frozen }

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	ids := make([]string, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release together, to actually contend
			snap, err := env.mgr.Create("race", "concurrent", false)
			errs[i], ids[i] = err, snap.ID
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent snapshot %d failed: %v", i, err)
			continue
		}
		if seen[ids[i]] {
			t.Errorf("id %s was handed out twice", ids[i])
		}
		seen[ids[i]] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct ids from %d concurrent snapshots", len(seen), n)
	}

	// Every recorded snapshot must have its archive on disk under its own
	// name — a rename that lost a race would leave a row pointing at nothing.
	for id := range seen {
		snap, err := env.store.GetSnapshot(id)
		if err != nil {
			t.Errorf("snapshot %s is not in the store: %v", id, err)
			continue
		}
		if _, err := os.Stat(snap.ZipPath); err != nil {
			t.Errorf("snapshot %s records %s, which does not exist: %v", id, snap.ZipPath, err)
		}
	}

	// And no staging files left behind.
	entries, err := os.ReadDir(filepath.Join(env.backups, "race", "main"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("staging archive left behind: %s", e.Name())
		}
	}
}
