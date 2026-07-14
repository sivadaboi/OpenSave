package legacyimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// fixtureJSON mirrors a real opensave-db.json written by the JS app,
// including a stale ".savesync" zipPath (user who never re-ran the JS app
// after its own directory migration) and per-peer sync lineage state.
const fixtureJSON = `{
  "settings": {
    "deviceName": "GAMING-PC",
    "nodeId": "node_aabbccddeeff00112233445566778899",
    "deviceType": "desktop",
    "port": 8383,
    "syncInterval": 5000,
    "syncOnWatch": true,
    "dataDir": "C:\\Users\\Siva\\.opensave",
    "backupsDir": "C:\\Users\\Siva\\.opensave\\backups",
    "syncBackupsDir": "C:\\Users\\Siva\\.opensave\\backups",
    "autoDeleteBackups": false,
    "autoDeleteDays": 30,
    "autoSyncOnTrack": false,
    "customScanPaths": ["D:\\Games"],
    "pathTranslations": [{"fromPattern": "C:\\Saves", "toPattern": "/home/deck/saves"}],
    "relayUrl": "wss://opensave-relay.onrender.com",
    "syncCode": "my-room-42",
    "hostRelay": false,
    "relayPort": 8386,
    "startOnBoot": true,
    "speedLimit": 250,
    "uiMode": "modern",
    "cloudSync": {
      "enabled": true,
      "provider": "dropbox",
      "url": "",
      "username": "",
      "password": "",
      "headers": "{}",
      "folderId": "",
      "customClientIds": {"google_drive": "", "onedrive": "", "dropbox": "custom-id"},
      "customClientSecrets": {"google_drive": "", "onedrive": "", "dropbox": ""},
      "tokens": {
        "accessToken": "at-123",
        "refreshToken": "rt-456",
        "expiryTime": 1780000000000,
        "userEmail": "user@example.com"
      }
    }
  },
  "games": {
    "elden-ring": {
      "id": "elden-ring",
      "name": "Elden Ring",
      "savePath": "C:\\Users\\Siva\\AppData\\Roaming\\EldenRing",
      "activeBranch": "ng-plus",
      "autoSync": true,
      "maxSnapshots": 10,
      "appId": "1245620",
      "branches": {
        "main": {
          "name": "main",
          "snapshots": [
            {
              "id": "snap_1700000000001",
              "timestamp": "2026-05-01T10:00:00.000Z",
              "comment": "before boss",
              "isSystemAuto": false,
              "zipPath": "C:\\Users\\Siva\\.savesync\\backups\\elden-ring\\main\\snap_1700000000001.zip",
              "sizeBytes": 123456,
              "branch": "main"
            }
          ]
        },
        "ng-plus": {
          "name": "ng-plus",
          "snapshots": [
            {
              "id": "snap_1700000000002",
              "timestamp": "2026-06-01T10:00:00.000Z",
              "comment": "",
              "isSystemAuto": true,
              "zipPath": "C:\\Users\\Siva\\.opensave\\backups\\elden-ring\\ng-plus\\snap_1700000000002.zip",
              "sizeBytes": 654321,
              "branch": "ng-plus"
            }
          ]
        }
      },
      "createdAt": "2026-01-15T08:00:00.000Z",
      "lastSyncedFilesByPeer": {
        "node_peer1": ["ER0000.sl2", "steam_autocloud.vdf"]
      },
      "lastSyncedDirsByPeer": {
        "node_peer1": ["GraphicsConfig"]
      }
    }
  },
  "peers": {
    "node_peer1": {
      "id": "node_peer1",
      "name": "Steam Deck",
      "deviceType": "deck",
      "address": "192.168.1.77",
      "port": 8383,
      "pairedAt": "2026-02-01T12:00:00.000Z",
      "lastSynced": "2026-06-15T09:30:00.000Z",
      "status": "online"
    }
  }
}`

func setupFixture(t *testing.T) (jsonPath, sqlitePath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	jsonPath = filepath.Join(dir, "opensave-db.json")
	sqlitePath = filepath.Join(dir, "opensave.db")
	logPath = filepath.Join(dir, "migration.log")
	if err := os.WriteFile(jsonPath, []byte(fixtureJSON), 0o666); err != nil {
		t.Fatal(err)
	}
	return jsonPath, sqlitePath, logPath
}

func TestNeeded(t *testing.T) {
	jsonPath, sqlitePath, _ := setupFixture(t)

	if !Needed(jsonPath, sqlitePath) {
		t.Error("Needed should be true when JSON exists and SQLite does not")
	}
	if err := os.WriteFile(sqlitePath, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if Needed(jsonPath, sqlitePath) {
		t.Error("Needed should be false once the SQLite file exists")
	}
	if Needed(filepath.Join(t.TempDir(), "missing.json"), filepath.Join(t.TempDir(), "new.db")) {
		t.Error("Needed should be false when there is no legacy JSON (fresh install)")
	}
}

func TestRun_FullImport(t *testing.T) {
	jsonPath, sqlitePath, logPath := setupFixture(t)

	if err := Run(jsonPath, sqlitePath, logPath); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	s, err := store.Open(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	settings, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.NodeID != "node_aabbccddeeff00112233445566778899" {
		t.Errorf("nodeId not preserved byte-for-byte: %q", settings.NodeID)
	}
	if settings.DeviceName != "GAMING-PC" || settings.SyncCode != "my-room-42" || settings.SpeedLimitKbps != 250 {
		t.Errorf("settings fields wrong: %+v", settings)
	}
	if settings.AutoSyncOnTrack {
		t.Error("autoSyncOnTrack=false in fixture must not be replaced by the true default")
	}
	if len(settings.PathTranslations) != 1 || settings.PathTranslations[0].ToPattern != "/home/deck/saves" {
		t.Errorf("pathTranslations wrong: %+v", settings.PathTranslations)
	}

	cloud, err := s.GetCloudConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cloud.Enabled || cloud.Provider != "dropbox" || cloud.AccessToken != "at-123" || cloud.RefreshToken != "rt-456" {
		t.Errorf("cloud config wrong: %+v", cloud)
	}
	if cloud.CustomClientIDs["dropbox"] != "custom-id" {
		t.Errorf("customClientIds wrong: %+v", cloud.CustomClientIDs)
	}

	game, err := s.GetGame("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if game.ActiveBranch != "ng-plus" || game.MaxSnapshots != 10 || game.AppID != "1245620" {
		t.Errorf("game fields wrong: %+v", game)
	}

	branches, err := s.ListBranches("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 {
		t.Errorf("expected 2 branches, got %v", branches)
	}

	mainSnaps, err := s.ListSnapshots("elden-ring", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(mainSnaps) != 1 {
		t.Fatalf("expected 1 snapshot on main, got %d", len(mainSnaps))
	}
	// The stale .savesync zipPath must get the same substitution db.js applies.
	wantZip := `C:\Users\Siva\.opensave\backups\elden-ring\main\snap_1700000000001.zip`
	if mainSnaps[0].ZipPath != wantZip {
		t.Errorf("zipPath = %q, want %q", mainSnaps[0].ZipPath, wantZip)
	}
	if mainSnaps[0].Comment != "before boss" || mainSnaps[0].IsSystemAuto {
		t.Errorf("snapshot metadata wrong: %+v", mainSnaps[0])
	}

	ngSnaps, err := s.ListSnapshots("elden-ring", "ng-plus")
	if err != nil {
		t.Fatal(err)
	}
	if len(ngSnaps) != 1 || !ngSnaps[0].IsSystemAuto {
		t.Errorf("ng-plus snapshots wrong: %+v", ngSnaps)
	}

	peer, err := s.GetPeer("node_peer1")
	if err != nil {
		t.Fatal(err)
	}
	if peer.Name != "Steam Deck" || peer.DeviceType != "deck" || peer.PairedAt != "2026-02-01T12:00:00.000Z" {
		t.Errorf("peer fields wrong: %+v", peer)
	}
	if peer.Status != "offline" {
		t.Errorf("imported peer status should start offline (runtime state), got %q", peer.Status)
	}
	if !peer.LastSynced.Valid || peer.LastSynced.String != "2026-06-15T09:30:00.000Z" {
		t.Errorf("peer lastSynced wrong: %+v", peer.LastSynced)
	}

	files, dirs, err := s.GetSyncState("elden-ring", "node_peer1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || len(dirs) != 1 || dirs[0] != "GraphicsConfig" {
		t.Errorf("sync lineage state wrong: files=%v dirs=%v", files, dirs)
	}

	// JSON must be renamed to a backup, not deleted.
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Error("legacy JSON should have been renamed away")
	}
	if _, err := os.Stat(jsonPath + ".migrated-backup"); err != nil {
		t.Errorf("expected .migrated-backup file: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected migration log: %v", err)
	}
}

func TestRun_CorruptJSONLeavesEverythingUntouched(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "opensave-db.json")
	sqlitePath := filepath.Join(dir, "opensave.db")
	if err := os.WriteFile(jsonPath, []byte("{not valid json"), 0o666); err != nil {
		t.Fatal(err)
	}

	err := Run(jsonPath, sqlitePath, filepath.Join(dir, "migration.log"))
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}

	if _, statErr := os.Stat(jsonPath); statErr != nil {
		t.Error("corrupt JSON must be left in place for the user to recover")
	}
	if _, statErr := os.Stat(sqlitePath); !os.IsNotExist(statErr) {
		t.Error("no SQLite file should remain after a failed import")
	}
}

func TestRun_MissingNodeIDRefusesImport(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "opensave-db.json")
	sqlitePath := filepath.Join(dir, "opensave.db")
	if err := os.WriteFile(jsonPath, []byte(`{"settings":{"deviceName":"X"},"games":{},"peers":{}}`), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := Run(jsonPath, sqlitePath, filepath.Join(dir, "migration.log")); err == nil {
		t.Fatal("expected refusal when legacy DB has no nodeId")
	}
	if _, statErr := os.Stat(sqlitePath); !os.IsNotExist(statErr) {
		t.Error("no SQLite file should remain after a refused import")
	}
}
