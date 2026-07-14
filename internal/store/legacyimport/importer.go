// Package legacyimport reads the original Node.js daemon's JSON database
// (~/.opensave/opensave-db.json, written by src/daemon/db.js) and imports
// it into the new SQLite store, exactly once, on first launch of the Go
// app on a machine that previously ran the JS app.
//
// Safety rules (do not weaken these):
//   - The JSON file is never modified or deleted on failure; on success it
//     is renamed to *.migrated-backup so the user can roll back to the JS
//     app if needed.
//   - nodeId is preserved byte-for-byte: peers on other machines key their
//     pairing records by it, so regenerating it would silently break every
//     existing pairing.
//   - Snapshot zipPath values are copied verbatim (after the same
//     ".savesync" -> ".opensave" substitution db.js itself performs),
//     since the physical backup files do not move.
package legacyimport

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// legacyDB mirrors the exact JSON shape of opensave-db.json.
type legacyDB struct {
	Settings legacySettings         `json:"settings"`
	Games    map[string]legacyGame  `json:"games"`
	Peers    map[string]legacyPeer  `json:"peers"`
}

type legacySettings struct {
	DeviceName        string   `json:"deviceName"`
	NodeID            string   `json:"nodeId"`
	DeviceType        string   `json:"deviceType"`
	Port              int      `json:"port"`
	SyncInterval      int      `json:"syncInterval"`
	SyncOnWatch       *bool    `json:"syncOnWatch"`
	DataDir           string   `json:"dataDir"`
	BackupsDir        string   `json:"backupsDir"`
	SyncBackupsDir    string   `json:"syncBackupsDir"`
	AutoDeleteBackups bool     `json:"autoDeleteBackups"`
	AutoDeleteDays    int      `json:"autoDeleteDays"`
	AutoSyncOnTrack   *bool    `json:"autoSyncOnTrack"`
	CustomScanPaths   []string `json:"customScanPaths"`
	PathTranslations  []store.TranslationRule `json:"pathTranslations"`
	RelayURL          string   `json:"relayUrl"`
	SyncCode          string   `json:"syncCode"`
	HostRelay         bool     `json:"hostRelay"`
	RelayPort         int      `json:"relayPort"`
	StartOnBoot       bool     `json:"startOnBoot"`
	SpeedLimit        int      `json:"speedLimit"`
	UIMode            string   `json:"uiMode"`
	CloudSync         legacyCloudSync `json:"cloudSync"`
}

type legacyCloudSync struct {
	Enabled             bool              `json:"enabled"`
	Provider            string            `json:"provider"`
	URL                 string            `json:"url"`
	Username            string            `json:"username"`
	Password            string            `json:"password"`
	Headers             string            `json:"headers"`
	FolderID            string            `json:"folderId"`
	CustomClientIDs     map[string]string `json:"customClientIds"`
	CustomClientSecrets map[string]string `json:"customClientSecrets"`
	Tokens              legacyTokens      `json:"tokens"`
}

type legacyTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiryTime   int64  `json:"expiryTime"`
	UserEmail    string `json:"userEmail"`
}

type legacyGame struct {
	ID                    string                   `json:"id"`
	Name                  string                   `json:"name"`
	SavePath              string                   `json:"savePath"`
	ActiveBranch          string                   `json:"activeBranch"`
	AutoSync              *bool                    `json:"autoSync"`
	MaxSnapshots          int                      `json:"maxSnapshots"`
	AppID                 string                   `json:"appId"`
	ExePath               string                   `json:"exePath"`
	CoverURL              string                   `json:"coverUrl"`
	Branches              map[string]legacyBranch  `json:"branches"`
	CreatedAt             string                   `json:"createdAt"`
	LastSyncedFilesByPeer map[string][]string      `json:"lastSyncedFilesByPeer"`
	LastSyncedDirsByPeer  map[string][]string      `json:"lastSyncedDirsByPeer"`
}

type legacyBranch struct {
	Name      string           `json:"name"`
	Snapshots []legacySnapshot `json:"snapshots"`
}

type legacySnapshot struct {
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	Comment      string `json:"comment"`
	IsSystemAuto bool   `json:"isSystemAuto"`
	ZipPath      string `json:"zipPath"`
	SizeBytes    int64  `json:"sizeBytes"`
	Branch       string `json:"branch"`
}

// Needed reports whether a legacy import should run: the JSON database
// exists and the SQLite database does not.
func Needed(legacyJSONPath, sqlitePath string) bool {
	if _, err := os.Stat(legacyJSONPath); err != nil {
		return false
	}
	if _, err := os.Stat(sqlitePath); err == nil {
		return false
	}
	return true
}

// Run imports the legacy JSON database into a freshly created SQLite store
// at sqlitePath. On any error the partially-written SQLite file is removed
// and the JSON file left untouched. On success the JSON file is renamed to
// <name>.migrated-backup and a summary appended to migrationLogPath.
func Run(legacyJSONPath, sqlitePath, migrationLogPath string) (retErr error) {
	raw, err := os.ReadFile(legacyJSONPath)
	if err != nil {
		return fmt.Errorf("read legacy database: %w", err)
	}

	var legacy legacyDB
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("parse legacy database (leaving JSON untouched): %w", err)
	}
	if legacy.Settings.NodeID == "" {
		return fmt.Errorf("legacy database has no nodeId — refusing to import (leaving JSON untouched)")
	}

	s, err := store.Open(sqlitePath)
	if err != nil {
		return fmt.Errorf("create sqlite database: %w", err)
	}

	defer func() {
		s.Close()
		if retErr != nil {
			os.Remove(sqlitePath)
		}
	}()

	if err := importAll(s, legacy); err != nil {
		return err
	}

	backupPath := legacyJSONPath + ".migrated-backup"
	if err := os.Rename(legacyJSONPath, backupPath); err != nil {
		// Import itself succeeded; a failed rename is not worth failing the
		// whole migration over (the Needed() check keys on the SQLite file
		// existing, so the import won't re-run). Log and continue.
		appendLog(migrationLogPath, fmt.Sprintf("WARN: could not rename legacy JSON to backup: %v", err))
	}

	appendLog(migrationLogPath, fmt.Sprintf(
		"migrated %d games, %d peers from %s (nodeId %s preserved); legacy JSON renamed to %s",
		len(legacy.Games), len(legacy.Peers), legacyJSONPath, legacy.Settings.NodeID, backupPath))
	return nil
}

func importAll(s *store.Store, legacy legacyDB) error {
	// The old db.js rewrote ".savesync" paths on load; replicate that here
	// in case this JSON predates the JS app's own directory migration.
	fixPath := func(p string) string {
		return strings.Replace(p, ".savesync", ".opensave", 1)
	}

	settings := store.Settings{
		DeviceName:        stringOr(legacy.Settings.DeviceName, "Unknown Device"),
		NodeID:            legacy.Settings.NodeID,
		DeviceType:        stringOr(legacy.Settings.DeviceType, "desktop"),
		Port:              intOr(legacy.Settings.Port, 8383),
		SyncInterval:      intOr(legacy.Settings.SyncInterval, 5000),
		SyncOnWatch:       boolOr(legacy.Settings.SyncOnWatch, true),
		DataDir:           fixPath(legacy.Settings.DataDir),
		BackupsDir:        fixPath(legacy.Settings.BackupsDir),
		SyncBackupsDir:    fixPath(legacy.Settings.SyncBackupsDir),
		AutoDeleteBackups: legacy.Settings.AutoDeleteBackups,
		AutoDeleteDays:    intOr(legacy.Settings.AutoDeleteDays, 30),
		AutoSyncOnTrack:   boolOr(legacy.Settings.AutoSyncOnTrack, true),
		CustomScanPaths:   legacy.Settings.CustomScanPaths,
		PathTranslations:  legacy.Settings.PathTranslations,
		RelayURL:          stringOr(legacy.Settings.RelayURL, "wss://opensave-relay.onrender.com"),
		SyncCode:          legacy.Settings.SyncCode,
		HostRelay:         legacy.Settings.HostRelay,
		RelayPort:         intOr(legacy.Settings.RelayPort, 8386),
		StartOnBoot:       legacy.Settings.StartOnBoot,
		SpeedLimitKbps:    legacy.Settings.SpeedLimit,
		UIMode:            stringOr(legacy.Settings.UIMode, "modern"),
	}
	if err := s.ImportSettings(settings); err != nil {
		return fmt.Errorf("import settings: %w", err)
	}

	cloud := store.CloudConfig{
		Enabled:             legacy.Settings.CloudSync.Enabled,
		Provider:            stringOr(legacy.Settings.CloudSync.Provider, "local"),
		URL:                 legacy.Settings.CloudSync.URL,
		Username:            legacy.Settings.CloudSync.Username,
		Password:            legacy.Settings.CloudSync.Password,
		HeadersJSON:         stringOr(legacy.Settings.CloudSync.Headers, "{}"),
		FolderID:            legacy.Settings.CloudSync.FolderID,
		CustomClientIDs:     legacy.Settings.CloudSync.CustomClientIDs,
		CustomClientSecrets: legacy.Settings.CloudSync.CustomClientSecrets,
		AccessToken:         legacy.Settings.CloudSync.Tokens.AccessToken,
		RefreshToken:        legacy.Settings.CloudSync.Tokens.RefreshToken,
		ExpiryTimeMs:        legacy.Settings.CloudSync.Tokens.ExpiryTime,
		UserEmail:           legacy.Settings.CloudSync.Tokens.UserEmail,
	}
	if err := s.UpdateCloudConfig(cloud); err != nil {
		return fmt.Errorf("import cloud config: %w", err)
	}

	for gameID, g := range legacy.Games {
		game := store.Game{
			ID:           stringOr(g.ID, gameID),
			Name:         g.Name,
			SavePath:     g.SavePath,
			ActiveBranch: stringOr(g.ActiveBranch, "main"),
			AutoSync:     boolOr(g.AutoSync, true),
			MaxSnapshots: intOr(g.MaxSnapshots, 5),
			AppID:        g.AppID,
			ExePath:      g.ExePath,
			CoverURL:     g.CoverURL,
		}
		if err := s.CreateGame(game); err != nil {
			return fmt.Errorf("import game %s: %w", gameID, err)
		}

		for branchName, branch := range g.Branches {
			// CreateGame already created the active branch; create the rest.
			if branchName != game.ActiveBranch {
				if err := s.CreateBranch(game.ID, branchName); err != nil {
					return fmt.Errorf("import branch %s/%s: %w", gameID, branchName, err)
				}
			}
			for _, snap := range branch.Snapshots {
				snapshot := store.Snapshot{
					ID:           snap.ID,
					GameID:       game.ID,
					BranchName:   branchName,
					Timestamp:    stringOr(snap.Timestamp, time.Now().UTC().Format(time.RFC3339)),
					Comment:      snap.Comment,
					IsSystemAuto: snap.IsSystemAuto,
					ZipPath:      fixPath(snap.ZipPath),
					SizeBytes:    snap.SizeBytes,
				}
				if err := s.CreateSnapshot(snapshot); err != nil {
					return fmt.Errorf("import snapshot %s of %s/%s: %w", snap.ID, gameID, branchName, err)
				}
			}
		}

		for peerID, files := range g.LastSyncedFilesByPeer {
			dirs := g.LastSyncedDirsByPeer[peerID]
			if err := s.SetSyncState(game.ID, peerID, files, dirs); err != nil {
				return fmt.Errorf("import sync state %s/%s: %w", gameID, peerID, err)
			}
		}
		// Dir-only entries (peer present in dirs map but not files map).
		for peerID, dirs := range g.LastSyncedDirsByPeer {
			if _, done := g.LastSyncedFilesByPeer[peerID]; done {
				continue
			}
			if err := s.SetSyncState(game.ID, peerID, nil, dirs); err != nil {
				return fmt.Errorf("import sync state %s/%s: %w", gameID, peerID, err)
			}
		}
	}

	for peerID, p := range legacy.Peers {
		peer := store.Peer{
			ID:         stringOr(p.ID, peerID),
			Name:       p.Name,
			DeviceType: stringOr(p.DeviceType, "desktop"),
			Address:    p.Address,
			Port:       intOr(p.Port, 8383),
			Status:     "offline", // status is runtime state; start every imported peer offline
		}
		if err := s.UpsertPeer(peer); err != nil {
			return fmt.Errorf("import peer %s: %w", peerID, err)
		}
		if p.PairedAt != "" {
			if err := s.SetPeerPairedAt(peer.ID, p.PairedAt); err != nil {
				return fmt.Errorf("import peer pairedAt %s: %w", peerID, err)
			}
		}
		if p.LastSynced != nil && *p.LastSynced != "" {
			if err := s.UpdatePeerLastSynced(peer.ID, *p.LastSynced); err != nil {
				return fmt.Errorf("import peer lastSynced %s: %w", peerID, err)
			}
		}
	}

	return nil
}

type legacyPeer struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	DeviceType string  `json:"deviceType"`
	Address    string  `json:"address"`
	Port       int     `json:"port"`
	PairedAt   string  `json:"pairedAt"`
	LastSynced *string `json:"lastSynced"`
	Status     string  `json:"status"`
}

func appendLog(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line)
}

func stringOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func intOr(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func boolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}
