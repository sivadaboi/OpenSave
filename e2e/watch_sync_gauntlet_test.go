package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// The gauntlet: end-to-end verification that file watching and syncing
// work as one system. Every test here runs two full daemons (API, watcher,
// snapshots, LAN P2P) — the same stack a real install runs.

// waitTracked waits until a game's initial (async) snapshot exists — the
// watcher attaches right after it, so writes made after this are watcher
// territory.
func waitTracked(t *testing.T, td *testutil.TestDaemon, gameID string) {
	t.Helper()
	if !testutil.WaitFor(15*time.Second, func() bool {
		snaps, err := td.Daemon.Store.ListSnapshots(gameID, "main")
		return err == nil && len(snaps) >= 1
	}) {
		t.Fatalf("initial snapshot for %s never appeared", gameID)
	}
	time.Sleep(500 * time.Millisecond) // watcher attach follows the snapshot
}

func snapshotCount(t *testing.T, td *testutil.TestDaemon, gameID string) int {
	t.Helper()
	snaps, err := td.Daemon.Store.ListSnapshots(gameID, "main")
	if err != nil {
		return 0
	}
	return len(snaps)
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// TestWatchAutoSyncChain is the headline test: a file written on A must
// arrive on B with ZERO manual sync calls — watcher → debounce →
// auto-snapshot → auto-sync → peer pull, the whole production chain.
func TestWatchAutoSyncChain(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Chain-A")
	b := testutil.NewTestDaemon(t, "Chain-B")
	a.PairWith(b)

	a.WriteSave("seed.sav", "seed")
	gameID := a.TrackGame("Chain Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Chain Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)

	// Creation propagates hands-free.
	a.WriteSave("auto.sav", "created by watcher")
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("auto.sav") == "created by watcher"
	}) {
		t.Fatal("new file never auto-synced to B (watch→snapshot→sync chain broken)")
	}

	// Modification propagates hands-free.
	time.Sleep(1100 * time.Millisecond) // mtime second boundary
	a.WriteSave("auto.sav", "modified v2")
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("auto.sav") == "modified v2"
	}) {
		t.Fatal("modification never auto-synced to B")
	}

	// Deletion propagates hands-free.
	if err := os.Remove(filepath.Join(a.SaveDir, "auto.sav")); err != nil {
		t.Fatal(err)
	}
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("auto.sav") == ""
	}) {
		t.Fatal("deletion never auto-synced to B")
	}
}

// TestWatcherNewSubdirectory: directories created AFTER the watch started
// must still be watched (fsnotify is non-recursive; the run loop extends
// the watch on Create).
func TestWatcherNewSubdirectory(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Subdir-A")
	b := testutil.NewTestDaemon(t, "Subdir-B")
	a.PairWith(b)

	a.WriteSave("root.sav", "root")
	gameID := a.TrackGame("Subdir Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Subdir Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)

	// Brand-new nested directories, then a file inside them.
	deep := filepath.Join(a.SaveDir, "profiles", "slot1")
	if err := os.MkdirAll(deep, 0o777); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second) // let the watcher pick up the new dirs
	if err := os.WriteFile(filepath.Join(deep, "deep.sav"), []byte("nested"), 0o666); err != nil {
		t.Fatal(err)
	}

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("profiles/slot1/deep.sav") == "nested"
	}) {
		t.Fatal("file in a post-watch subdirectory never synced — recursive watch extension broken")
	}
}

// TestWatcherDebounce: a burst of writes must coalesce into very few
// snapshots, not one per write.
func TestWatcherDebounce(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Debounce-A")

	a.WriteSave("d.sav", "start")
	gameID := a.TrackGame("Debounce Game")
	waitTracked(t, a, gameID)
	before := snapshotCount(t, a, gameID)

	for i := 0; i < 20; i++ {
		a.WriteSave("d.sav", fmt.Sprintf("burst-%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for the debounce window + snapshot to settle.
	if !testutil.WaitFor(30*time.Second, func() bool {
		return snapshotCount(t, a, gameID) > before
	}) {
		t.Fatal("burst of writes produced no snapshot at all")
	}
	time.Sleep(5 * time.Second) // any stragglers
	made := snapshotCount(t, a, gameID) - before
	if made > 3 {
		t.Errorf("20 rapid writes produced %d snapshots — debounce not coalescing", made)
	}
}

// TestSyncContentIntegrity: hostile filenames and binary content must
// arrive bit-perfect.
func TestSyncContentIntegrity(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Integrity-A")
	b := testutil.NewTestDaemon(t, "Integrity-B")
	a.PairWith(b)

	// Binary payloads including a multi-megabyte one.
	big := make([]byte, 5<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"plain.sav":                       []byte("plain"),
		"with spaces (slot 2).bin":        {0x00, 0xFF, 0x10, 0x00, 0x7F},
		"セーブデータ.dat":                      []byte("japanese filename"),
		"émoji💾.sav":                      []byte("unicode"),
		"deep/nested/dirs/save.bin":       big,
		"UPPER_lower.MiXeD":               []byte("case"),
		"dots.in.name.v1.2.sav":           []byte("dots"),
		"config/settings with spaces.ini": []byte("ini"),
	}
	for rel, content := range files {
		p := filepath.Join(a.SaveDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o666); err != nil {
			t.Fatal(err)
		}
	}

	gameID := a.TrackGame("Integrity Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Integrity Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(60*time.Second, func() bool {
		for rel, content := range files {
			got, err := os.ReadFile(filepath.Join(b.SaveDir, filepath.FromSlash(rel)))
			if err != nil || !bytes.Equal(got, content) {
				return false
			}
		}
		return true
	}) {
		for rel, content := range files {
			p := filepath.Join(b.SaveDir, filepath.FromSlash(rel))
			want := fmt.Sprintf("%x", sha256.Sum256(content))[:12]
			t.Errorf("%s: want sha %s…, got %s… ", rel, want, fileSHA(t, p)[:12])
		}
		t.Fatal("content integrity mismatch after sync")
	}
}

// TestSyncManyFiles: hundreds of files in one pass.
func TestSyncManyFiles(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Many-A")
	b := testutil.NewTestDaemon(t, "Many-B")
	a.PairWith(b)

	const n = 200
	for i := 0; i < n; i++ {
		a.WriteSave(fmt.Sprintf("chunk%02d/file%03d.sav", i%10, i), fmt.Sprintf("content-%d", i))
	}
	gameID := a.TrackGame("Many Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Many Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(90*time.Second, func() bool {
		for i := 0; i < n; i += 17 { // spot-check across the range
			if b.ReadSave(fmt.Sprintf("chunk%02d/file%03d.sav", i%10, i)) != fmt.Sprintf("content-%d", i) {
				return false
			}
		}
		return b.ReadSave(fmt.Sprintf("chunk%02d/file%03d.sav", (n-1)%10, n-1)) == fmt.Sprintf("content-%d", n-1)
	}) {
		t.Fatal("200-file sync incomplete after 90s")
	}
}

// TestReverseDeletionPropagation: B deletes, A must drop the file too.
func TestReverseDeletionPropagation(t *testing.T) {
	a := testutil.NewTestDaemon(t, "RevDel-A")
	b := testutil.NewTestDaemon(t, "RevDel-B")
	a.PairWith(b)

	a.WriteSave("kill-me.sav", "here")
	a.WriteSave("keep-me.sav", "staying")
	gameID := a.TrackGame("RevDel Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "RevDel Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("kill-me.sav") == "here"
	}) {
		t.Fatal("initial sync to B failed")
	}

	if err := os.Remove(filepath.Join(b.SaveDir, "kill-me.sav")); err != nil {
		t.Fatal(err)
	}
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return a.ReadSave("kill-me.sav") == ""
	}) {
		t.Fatal("B's deletion never propagated back to A")
	}
	if a.ReadSave("keep-me.sav") != "staying" {
		t.Fatal("unrelated file was deleted alongside — deletion overreach")
	}
}

// TestSingleFileGameChain: single-file save mode through the full auto
// chain, including the safe-write pattern (write tmp, rename over).
func TestSingleFileGameChain(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Single-A")
	b := testutil.NewTestDaemon(t, "Single-B")
	a.PairWith(b)

	aFile := filepath.Join(a.SaveDir, "solo.sav")
	if err := os.WriteFile(aFile, []byte("v1"), 0o666); err != nil {
		t.Fatal(err)
	}
	var game struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games", map[string]string{"name": "Solo Game", "savePath": aFile}, &game)
	bFile := filepath.Join(b.SaveDir, "solo.sav")
	if err := os.WriteFile(bFile, []byte("v1"), 0o666); err != nil {
		t.Fatal(err)
	}
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Solo Game", "savePath": bFile}, nil)
	waitTracked(t, a, game.ID)

	// Establish lineage with one initial sync (contents are identical, so
	// this records in_sync) — without it, the engine correctly treats two
	// never-synced copies as a conflict rather than guessing.
	a.API(http.MethodPost, "/api/games/"+game.ID+"/sync", nil, nil)
	time.Sleep(2 * time.Second)

	// Safe-write: new content lands via rename, like real games do.
	time.Sleep(1100 * time.Millisecond)
	tmp := aFile + ".swap"
	if err := os.WriteFile(tmp, []byte("v2-renamed"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, aFile); err != nil {
		t.Fatal(err)
	}

	if !testutil.WaitFor(45*time.Second, func() bool {
		got, _ := os.ReadFile(bFile)
		return string(got) == "v2-renamed"
	}) {
		t.Fatal("single-file safe-write never auto-synced to B")
	}
}

// TestQueuedSyncConsistency: a change made during an active sync must be
// delivered by the queued follow-up pass.
func TestQueuedSyncConsistency(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Queue-A")
	b := testutil.NewTestDaemon(t, "Queue-B")
	a.PairWith(b)

	// Enough data that the first sync isn't instantaneous.
	big := make([]byte, 8<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.SaveDir, "big.bin"), big, 0o666); err != nil {
		t.Fatal(err)
	}
	gameID := a.TrackGame("Queue Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Queue Game", "savePath": b.SaveDir}, nil)
	waitTracked(t, a, gameID)

	// Fire the sync, then immediately land another change behind it.
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	a.WriteSave("late-arrival.sav", "made mid-sync")
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil) // may queue

	if !testutil.WaitFor(60*time.Second, func() bool {
		got, _ := os.ReadFile(filepath.Join(b.SaveDir, "big.bin"))
		return bytes.Equal(got, big) && b.ReadSave("late-arrival.sav") == "made mid-sync"
	}) {
		t.Fatal("change made during an active sync was not delivered")
	}
}
