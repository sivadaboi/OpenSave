package syncengine

import (
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

func rootEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(store.Game{ID: "g", Name: "Game", SavePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: s, Log: func(string, string) {}}, s
}

func remoteWithRoots(proto int, roots map[string]delta.RootManifest) ManifestResponse {
	return ManifestResponse{
		Proto:    proto,
		Manifest: delta.Manifest{Files: map[string]delta.FileEntry{}, Extra: roots},
	}
}

// The gate. A peer that answered without a proto is on a build that ignores
// the root name in a block request and would serve the file from its PRIMARY
// location instead — this side would then write one folder's contents over
// another. No proto, no extra locations, however well they otherwise match.
func TestExtraLocationsAreNotSyncedToAPeerThatCannotName(t *testing.T) {
	e, s := rootEngine(t)
	if err := s.AddGameRoot("g", "config", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	roots := map[string]delta.RootManifest{"config": {Files: map[string]delta.FileEntry{"a.ini": {Hash: "x"}}}}

	if got := e.sharedRoots("g", remoteWithRoots(0, roots)); len(got) != 0 {
		t.Errorf("a peer with no proto was offered %d locations; it would serve them from its save folder", len(got))
	}
	if got := e.sharedRoots("g", remoteWithRoots(ProtoMultiRoot, roots)); len(got) != 1 {
		t.Errorf("a capable peer got %d shared locations, want 1", len(got))
	}
}

// A location this device knows the name of but has no path for cannot be
// written to. Syncing it would mean choosing a folder on the user's behalf.
func TestUnmappedLocationIsNotSynced(t *testing.T) {
	e, s := rootEngine(t)
	if err := s.AddGameRoot("g", "config", ""); err != nil {
		t.Fatal(err)
	}
	roots := map[string]delta.RootManifest{"config": {Files: map[string]delta.FileEntry{"a.ini": {Hash: "x"}}}}

	if got := e.sharedRoots("g", remoteWithRoots(ProtoMultiRoot, roots)); len(got) != 0 {
		t.Errorf("a location with no local path was synced (%d); there is nowhere to put the files", len(got))
	}
}

// A location the peer does not list is not empty on the peer, it is absent.
// Syncing against an empty manifest would read as "every file here was
// deleted" and propagate that back.
func TestLocationThePeerDoesNotHaveIsSkippedNotEmptied(t *testing.T) {
	e, s := rootEngine(t)
	if err := s.AddGameRoot("g", "mods", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Peer is capable, but lists a different location entirely.
	roots := map[string]delta.RootManifest{"config": {Files: map[string]delta.FileEntry{"a.ini": {Hash: "x"}}}}

	if got := e.sharedRoots("g", remoteWithRoots(ProtoMultiRoot, roots)); len(got) != 0 {
		t.Errorf("a location the peer never mentioned was synced (%d); its files would be deleted as absent upstream", len(got))
	}
}

// Nearly every game has one location, and this whole pass must be a no-op for
// them — no lookups, no work, no way to change how they behave.
func TestSingleRootGamesGetNoExtraPass(t *testing.T) {
	e, _ := rootEngine(t)
	roots := map[string]delta.RootManifest{"config": {Files: map[string]delta.FileEntry{"a.ini": {Hash: "x"}}}}

	if got := e.sharedRoots("g", remoteWithRoots(ProtoMultiRoot, roots)); len(got) != 0 {
		t.Errorf("a game with no extra locations produced %d; the peer's locations must not be adopted unasked", len(got))
	}
}

// Only the locations both sides have, and only those, take part.
func TestOnlyTheIntersectionIsSynced(t *testing.T) {
	e, s := rootEngine(t)
	for _, name := range []string{"config", "mods", "screenshots"} {
		if err := s.AddGameRoot("g", name, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	roots := map[string]delta.RootManifest{
		"config": {Files: map[string]delta.FileEntry{"a.ini": {Hash: "x"}}},
		"mods":   {Files: map[string]delta.FileEntry{"b.pak": {Hash: "y"}}},
		"cheats": {Files: map[string]delta.FileEntry{"c.txt": {Hash: "z"}}}, // peer only
	}

	got := e.sharedRoots("g", remoteWithRoots(ProtoMultiRoot, roots))
	if len(got) != 2 {
		t.Fatalf("shared locations = %d, want 2 (config and mods)", len(got))
	}
	names := map[string]bool{}
	for _, sr := range got {
		names[sr.root.Name] = true
		if sr.root.Path == "" {
			t.Errorf("location %q came through with no local path", sr.root.Name)
		}
	}
	if !names["config"] || !names["mods"] {
		t.Errorf("shared locations = %v, want config and mods", names)
	}
	if names["cheats"] {
		t.Error("a location only the peer has was adopted")
	}
	if names["screenshots"] {
		t.Error("a location only this device has was synced against nothing")
	}
}
