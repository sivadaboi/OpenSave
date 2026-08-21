package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

// The recorded hash and the manifest hash are compared against each other to
// answer "is this exact content already captured". If the two are computed
// differently — a different algorithm, a different encoding, a checksum of the
// archive entry rather than of the file — every lookup misses, silently, and
// the record is worse than useless: it costs a walk and answers nothing.
//
// So this is not a test that hashing works. It is a test that these two agree.
func TestCapturedHashesMatchTheManifest(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "save.sav", "some progress")
	writeSave(t, env.saveDir, "nested/deeper/config.ini", "fullscreen=1")

	out := filepath.Join(t.TempDir(), "snap.zip")
	_, captured, err := ZipRootsCapturing(env.saveDir, nil, out)
	if err != nil {
		t.Fatalf("ZipRootsCapturing: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured %d files, want 2: %+v", len(captured), captured)
	}

	manifest, err := delta.BuildManifest(env.saveDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range captured {
		entry, ok := manifest.Files[c.Path]
		if !ok {
			t.Errorf("captured %q under a path the manifest does not use; keys: %v",
				c.Path, keysOf(manifest.Files))
			continue
		}
		if c.Hash != entry.Hash {
			t.Errorf("%s: captured hash %s, manifest hash %s — lookups would never match",
				c.Path, c.Hash, entry.Hash)
		}
	}
}

// Extra locations are recorded under their own name. Without that, the same
// relative path in two locations would answer for each other, and a config
// file could be taken as proof that a save file was captured.
func TestCapturedFilesNameTheirLocation(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "primary")

	configDir := filepath.Join(t.TempDir(), "cfg")
	if err := os.MkdirAll(configDir, 0o777); err != nil {
		t.Fatal(err)
	}
	// Deliberately the same relative path as the primary file.
	if err := os.WriteFile(filepath.Join(configDir, "slot1.sav"), []byte("config"), 0o666); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "snap.zip")
	_, captured, err := ZipRootsCapturing(env.saveDir, map[string]string{"config": configDir}, out)
	if err != nil {
		t.Fatalf("ZipRootsCapturing: %v", err)
	}

	byRoot := map[string]store.CapturedFile{}
	for _, c := range captured {
		byRoot[c.Root] = c
	}
	primary, ok := byRoot[""]
	if !ok {
		t.Fatal("the primary location recorded nothing")
	}
	cfg, ok := byRoot["config"]
	if !ok {
		t.Fatal(`the "config" location recorded nothing`)
	}
	if primary.Path != cfg.Path {
		t.Fatalf("this test needs both to share a path, got %q and %q", primary.Path, cfg.Path)
	}
	if primary.Hash == cfg.Hash {
		t.Error("different contents produced the same hash")
	}
}

// A snapshot must record what it holds, so the two can never disagree.
func TestSnapshotRecordsItsFiles(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "save.sav", "v1")

	snap, err := env.mgr.Create("game1", "by hand", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	files, err := env.store.SnapshotFiles(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("the snapshot recorded no files, so it cannot show what it captured")
	}

	captured, err := env.store.IsContentCaptured("game1", "", files[0].Path, files[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !captured {
		t.Error("a file this snapshot holds does not read as captured")
	}

	// And content it never held must not read as captured.
	captured, err = env.store.IsContentCaptured("game1", "", files[0].Path, "0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if captured {
		t.Error("content no snapshot holds read as captured — this is the direction that loses saves")
	}
}

// Pruning takes the file list with the archive. A list outliving its snapshot
// would claim contents are recoverable from a zip that is gone, which is the
// one wrong answer with teeth: it is what lets a caller skip protecting a file.
func TestPrunedSnapshotTakesItsFileListWithIt(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "save.sav", "v1")
	snap, err := env.mgr.Create("game1", "one", false)
	if err != nil {
		t.Fatal(err)
	}
	if files, _ := env.store.SnapshotFiles(snap.ID); len(files) == 0 {
		t.Fatal("nothing recorded to begin with")
	}

	if err := env.store.DeleteSnapshot(snap.ID); err != nil {
		t.Fatal(err)
	}
	files, err := env.store.SnapshotFiles(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("%d file rows survived their snapshot", len(files))
	}
}

func keysOf(m map[string]delta.FileEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
