package store

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "opensave.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnsureDefaultSettings_CreatesRowOnceWithStableNodeID(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatalf("EnsureDefaultSettings() error = %v", err)
	}
	first, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeID == "" {
		t.Error("expected a generated nodeId")
	}

	// Calling again must be a no-op — nodeId must never change once set,
	// since peers on other devices key pairing records by it.
	if err := s.EnsureDefaultSettings("/data2", "/backups2"); err != nil {
		t.Fatalf("second EnsureDefaultSettings() error = %v", err)
	}
	second, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if second.NodeID != first.NodeID {
		t.Errorf("nodeId changed across EnsureDefaultSettings calls: %q -> %q", first.NodeID, second.NodeID)
	}
	if second.DataDir != "/data" {
		t.Errorf("DataDir should not be overwritten by the second call, got %q", second.DataDir)
	}
}

func TestUpdateSettings_RoundTripsJSONColumns(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}

	settings, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.CustomScanPaths = []string{"/mnt/games", "/mnt/emu"}
	settings.PathTranslations = []TranslationRule{{FromPattern: "C:\\Saves", ToPattern: "/saves"}}
	settings.SpeedLimitKbps = 500

	if err := s.UpdateSettings(settings); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CustomScanPaths) != 2 || got.CustomScanPaths[1] != "/mnt/emu" {
		t.Errorf("CustomScanPaths round-trip failed: %+v", got.CustomScanPaths)
	}
	if len(got.PathTranslations) != 1 || got.PathTranslations[0].ToPattern != "/saves" {
		t.Errorf("PathTranslations round-trip failed: %+v", got.PathTranslations)
	}
	if got.SpeedLimitKbps != 500 {
		t.Errorf("SpeedLimitKbps = %d, want 500", got.SpeedLimitKbps)
	}
}

func TestGameBranchSnapshotLifecycle(t *testing.T) {
	s := openTestStore(t)

	game := Game{ID: "elden-ring", Name: "Elden Ring", SavePath: `C:\Saves\EldenRing`}
	if err := s.CreateGame(game); err != nil {
		t.Fatalf("CreateGame() error = %v", err)
	}

	branches, err := s.ListBranches(game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0] != "main" {
		t.Errorf("expected default 'main' branch, got %v", branches)
	}

	if err := s.CreateBranch(game.ID, "ng-plus"); err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}
	if err := s.SwitchActiveBranch(game.ID, "ng-plus"); err != nil {
		t.Fatalf("SwitchActiveBranch() error = %v", err)
	}
	updated, err := s.GetGame(game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveBranch != "ng-plus" {
		t.Errorf("ActiveBranch = %q, want ng-plus", updated.ActiveBranch)
	}

	snap := Snapshot{ID: "snap_1", GameID: game.ID, BranchName: "ng-plus", Timestamp: "2026-01-01T00:00:00Z", ZipPath: "/backups/snap_1.zip"}
	if err := s.CreateSnapshot(snap); err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	snaps, err := s.ListSnapshots(game.ID, "ng-plus")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].ID != "snap_1" {
		t.Errorf("ListSnapshots = %+v, want one snapshot snap_1", snaps)
	}

	// Deleting the game should cascade-delete its branches and snapshots.
	if err := s.DeleteGame(game.ID); err != nil {
		t.Fatalf("DeleteGame() error = %v", err)
	}
	if _, err := s.GetGame(game.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
	remainingSnaps, err := s.ListSnapshots(game.ID, "ng-plus")
	if err != nil {
		t.Fatal(err)
	}
	if len(remainingSnaps) != 0 {
		t.Errorf("expected snapshots to cascade-delete with the game, got %+v", remainingSnaps)
	}
}

func TestSnapshotsBeyondRetention(t *testing.T) {
	s := openTestStore(t)
	game := Game{ID: "g1", Name: "Game", SavePath: "/save", MaxSnapshots: 2}
	if err := s.CreateGame(game); err != nil {
		t.Fatal(err)
	}
	timestamps := []string{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z"}
	for i, ts := range timestamps {
		snap := Snapshot{ID: "snap_" + string(rune('a'+i)), GameID: game.ID, BranchName: "main", Timestamp: ts, ZipPath: "/x.zip"}
		if err := s.CreateSnapshot(snap); err != nil {
			t.Fatal(err)
		}
	}

	beyond, err := s.SnapshotsBeyondRetention(game.ID, "main", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(beyond) != 1 || beyond[0].ID != "snap_a" {
		t.Errorf("expected only the oldest snapshot beyond retention, got %+v", beyond)
	}
}

func TestPeerPairingAndSyncStateLifecycle(t *testing.T) {
	s := openTestStore(t)
	peer := Peer{ID: "node_abc", Name: "Laptop", Address: "192.168.1.50", Port: 8383, Status: "online"}
	if err := s.UpsertPeer(peer); err != nil {
		t.Fatalf("UpsertPeer() error = %v", err)
	}

	files, dirs, err := s.GetSyncState("game1", peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 || len(dirs) != 0 {
		t.Errorf("expected empty sync state before first sync, got files=%v dirs=%v", files, dirs)
	}

	if err := s.SetSyncState("game1", peer.ID, []string{"save.dat", "sub/other.dat"}, []string{"sub"}); err != nil {
		t.Fatalf("SetSyncState() error = %v", err)
	}
	files, dirs, err = s.GetSyncState("game1", peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || len(dirs) != 1 {
		t.Errorf("sync state round-trip failed: files=%v dirs=%v", files, dirs)
	}

	if err := s.UnpairPeer(peer.ID); err != nil {
		t.Fatalf("UnpairPeer() error = %v", err)
	}
	if _, err := s.GetPeer(peer.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after unpair, got %v", err)
	}
	files, dirs, err = s.GetSyncState("game1", peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 || len(dirs) != 0 {
		t.Error("expected sync state to be cleared after unpair")
	}
}

func TestPeerLastSyncedWireShape(t *testing.T) {
	s := openTestStore(t)
	p := Peer{ID: "peer-1", Name: "Laptop", Address: "10.0.0.2", Port: 8383, Status: "online"}
	if err := s.UpsertPeer(p); err != nil {
		t.Fatal(err)
	}

	// Unsynced peer serializes lastSynced as null (not {String,Valid} —
	// that object rendered as "Invalid Date" in the dashboard).
	got, _ := s.GetPeer("peer-1")
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"lastSynced":null`)) {
		t.Errorf("unsynced peer JSON = %s, want lastSynced:null", raw)
	}

	if err := s.UpdatePeerLastSynced("peer-1", "2026-07-14T10:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPeer("peer-1")
	raw, _ = json.Marshal(got)
	if !bytes.Contains(raw, []byte(`"lastSynced":"2026-07-14T10:00:00.000Z"`)) {
		t.Errorf("synced peer JSON = %s, want plain lastSynced string", raw)
	}
}

func TestPrunePeersAtAddress(t *testing.T) {
	s := openTestStore(t)
	// Old identity of the machine at 10.0.0.2 (pre-reinstall)...
	if err := s.UpsertPeer(Peer{ID: "old-id", Name: "LAPTOP-OLD", Address: "10.0.0.2", Port: 8383, Status: "offline"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncState("game-1", "old-id", []string{"a.sav"}, nil); err != nil {
		t.Fatal(err)
	}
	// ...and an unrelated peer elsewhere.
	if err := s.UpsertPeer(Peer{ID: "other", Name: "Deck", Address: "10.0.0.9", Port: 8383, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	// The machine re-pairs under a fresh identity.
	if err := s.UpsertPeer(Peer{ID: "new-id", Name: "LAPTOP-NEW", Address: "10.0.0.2", Port: 8383, Status: "online"}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.PrunePeersAtAddress("10.0.0.2", 8383, "new-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "LAPTOP-OLD" {
		t.Fatalf("removed = %v, want [LAPTOP-OLD]", removed)
	}
	if _, err := s.GetPeer("old-id"); err == nil {
		t.Error("ghost peer should be gone")
	}
	if _, err := s.GetPeer("new-id"); err != nil {
		t.Error("new identity must survive")
	}
	if _, err := s.GetPeer("other"); err != nil {
		t.Error("unrelated peer must survive")
	}
	if files, _, _ := s.GetSyncState("game-1", "old-id"); len(files) != 0 {
		t.Error("ghost peer's sync lineage should be gone")
	}

	// "relay" is every WAN peer's pseudo-address — never prune on it.
	_ = s.UpsertPeer(Peer{ID: "wan-a", Name: "A", Address: "relay", Port: 1, Status: "online"})
	_ = s.UpsertPeer(Peer{ID: "wan-b", Name: "B", Address: "relay", Port: 1, Status: "online"})
	if removed, _ := s.PrunePeersAtAddress("relay", 1, "wan-a"); len(removed) != 0 {
		t.Errorf("must never prune on the relay pseudo-address, removed %v", removed)
	}
}
