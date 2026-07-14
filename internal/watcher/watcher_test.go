package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// collector implements the callback surface with in-memory bookkeeping.
type collector struct {
	mu            sync.Mutex
	snapshots     []string
	changed       []string
	manifestHashes map[string]string
	failSnapshots int // fail this many snapshot attempts before succeeding
}

func newCollector() *collector {
	return &collector{manifestHashes: map[string]string{}}
}

func (c *collector) callbacks() Callbacks {
	return Callbacks{
		GetLastManifestHash: func(gameID string) (string, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.manifestHashes[gameID], nil
		},
		SetLastManifestHash: func(gameID, hash string) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.manifestHashes[gameID] = hash
			return nil
		},
		CreateSnapshot: func(gameID string) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.failSnapshots > 0 {
				c.failSnapshots--
				return os.ErrPermission
			}
			c.snapshots = append(c.snapshots, gameID)
			return nil
		},
		OnChanged: func(gameID string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.changed = append(c.changed, gameID)
		},
	}
}

func (c *collector) snapshotCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snapshots)
}

func (c *collector) changedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.changed)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

func TestDirectoryWatch_SnapshotAfterDebounce(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(saveDir, "slot1.sav"), []byte("progress"), 0o666); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() >= 1 }) {
		t.Fatal("expected an auto-snapshot after the debounce window")
	}
	if !waitFor(t, 2*time.Second, func() bool { return col.changedCount() >= 1 }) {
		t.Fatal("expected OnChanged to fire after the snapshot")
	}
}

func TestDirectoryWatch_RapidWritesCollapseToOneSnapshot(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}

	// Burst of writes inside one debounce window.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(saveDir, "slot1.sav"), []byte{byte(i)}, 0o666); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() >= 1 }) {
		t.Fatal("expected a snapshot")
	}
	// Give it a beat to prove no extra snapshots trail in.
	time.Sleep(3 * time.Second)
	if got := col.snapshotCount(); got != 1 {
		t.Errorf("expected exactly 1 snapshot for a rapid burst, got %d", got)
	}
}

func TestSingleFileWatch_SurvivesSafeWriteReplace(t *testing.T) {
	dir := t.TempDir()
	saveFile := filepath.Join(dir, "profile.sav")
	if err := os.WriteFile(saveFile, []byte("v1"), 0o666); err != nil {
		t.Fatal(err)
	}

	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveFile); err != nil {
		t.Fatal(err)
	}

	// Safe-write pattern: write temp, delete original, rename temp over it.
	simulateSafeWrite := func(content string) {
		tmp := filepath.Join(dir, "profile.sav.tmp123")
		if err := os.WriteFile(tmp, []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(saveFile); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, saveFile); err != nil {
			t.Fatal(err)
		}
	}

	simulateSafeWrite("v2")
	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() >= 1 }) {
		t.Fatal("watcher should survive safe-write replace and snapshot (round 1)")
	}

	// The critical regression case: the watch must still be alive for a
	// SECOND safe-write cycle (the original JS bug killed the watch after
	// the first unlink).
	simulateSafeWrite("v3")
	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() >= 2 }) {
		t.Fatal("watcher died after the first safe-write cycle")
	}
}

func TestSingleFileWatch_IgnoresSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	saveFile := filepath.Join(dir, "profile.sav")
	if err := os.WriteFile(saveFile, []byte("v1"), 0o666); err != nil {
		t.Fatal(err)
	}

	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveFile); err != nil {
		t.Fatal(err)
	}

	// Unrelated sibling files must not trigger snapshots.
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("noise"), 0o666); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if got := col.snapshotCount(); got != 0 {
		t.Errorf("sibling file writes should not snapshot, got %d snapshots", got)
	}
}

func TestManifestHashDedup_NoSnapshotForIdenticalContent(t *testing.T) {
	saveDir := t.TempDir()
	savePath := filepath.Join(saveDir, "slot1.sav")
	if err := os.WriteFile(savePath, []byte("same content"), 0o666); err != nil {
		t.Fatal(err)
	}

	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}

	// First change: real snapshot.
	if err := os.WriteFile(savePath, []byte("new content"), 0o666); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() == 1 }) {
		t.Fatal("expected first snapshot")
	}

	// Touch the file with IDENTICAL content (e.g. a sync just wrote the
	// same bytes back): no second snapshot.
	if err := os.WriteFile(savePath, []byte("new content"), 0o666); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if got := col.snapshotCount(); got != 1 {
		t.Errorf("identical content must not re-snapshot (feedback loop), got %d", got)
	}
}

func TestSnapshotRetries(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	col.failSnapshots = 2 // first two attempts fail, third succeeds
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "slot1.sav"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 15*time.Second, func() bool { return col.snapshotCount() >= 1 }) {
		t.Fatal("snapshot should eventually succeed after retries")
	}
}

func TestUnwatchStopsEvents(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()

	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}
	eng.Unwatch("game1")

	if err := os.WriteFile(filepath.Join(saveDir, "slot1.sav"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if got := col.snapshotCount(); got != 0 {
		t.Errorf("no snapshots expected after Unwatch, got %d", got)
	}
}
