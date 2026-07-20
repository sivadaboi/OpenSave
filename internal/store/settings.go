package store

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
)

// TranslationRule mirrors delta.TranslationRule for storage purposes (kept
// as its own type here to avoid internal/store depending on internal/delta
// just for a two-field struct).
type TranslationRule struct {
	FromPattern string `json:"fromPattern"`
	ToPattern   string `json:"toPattern"`
}

// Settings is the singleton device configuration row.
type Settings struct {
	ID                int               `db:"id" json:"-"`
	DeviceName        string            `db:"device_name" json:"deviceName"`
	NodeID            string            `db:"node_id" json:"nodeId"`
	DeviceType        string            `db:"device_type" json:"deviceType"`
	Port              int               `db:"port" json:"port"`
	SyncInterval      int               `db:"sync_interval" json:"syncInterval"`
	SyncOnWatch       bool              `db:"sync_on_watch" json:"syncOnWatch"`
	DataDir           string            `db:"data_dir" json:"dataDir"`
	BackupsDir        string            `db:"backups_dir" json:"backupsDir"`
	SyncBackupsDir    string            `db:"sync_backups_dir" json:"syncBackupsDir"`
	AutoDeleteBackups bool              `db:"auto_delete_backups" json:"autoDeleteBackups"`
	AutoDeleteDays    int               `db:"auto_delete_days" json:"autoDeleteDays"`
	AutoSyncOnTrack   bool              `db:"auto_sync_on_track" json:"autoSyncOnTrack"`
	CustomScanPaths   []string          `db:"-" json:"customScanPaths"`
	PathTranslations  []TranslationRule `db:"-" json:"pathTranslations"`
	RelayURL          string            `db:"relay_url" json:"relayUrl"`
	SyncCode          string            `db:"sync_code" json:"syncCode"`
	HostRelay         bool              `db:"host_relay" json:"hostRelay"`
	RelayPort         int               `db:"relay_port" json:"relayPort"`
	StartOnBoot       bool              `db:"start_on_boot" json:"startOnBoot"`
	SpeedLimitKbps    int               `db:"speed_limit_kbps" json:"speedLimit"`
	UIMode            string            `db:"ui_mode" json:"uiMode"`
	DefaultMaxSnapshots int             `db:"default_max_snapshots" json:"defaultMaxSnapshots"`

	CustomScanPathsJSON  string `db:"custom_scan_paths" json:"-"`
	PathTranslationsJSON string `db:"path_translations" json:"-"`
	UpdatedAt            string `db:"updated_at" json:"-"`
}

// settingsRow exists purely so sqlx can scan the raw row (including the
// *_json columns) before we unmarshal them into Settings' slice fields.
func (s *Settings) unmarshalJSONColumns() error {
	if s.CustomScanPathsJSON != "" {
		if err := json.Unmarshal([]byte(s.CustomScanPathsJSON), &s.CustomScanPaths); err != nil {
			return fmt.Errorf("unmarshal customScanPaths: %w", err)
		}
	}
	if s.PathTranslationsJSON != "" {
		if err := json.Unmarshal([]byte(s.PathTranslationsJSON), &s.PathTranslations); err != nil {
			return fmt.Errorf("unmarshal pathTranslations: %w", err)
		}
	}
	return nil
}

func (s *Settings) marshalJSONColumns() error {
	scanPaths := s.CustomScanPaths
	if scanPaths == nil {
		scanPaths = []string{}
	}
	b, err := json.Marshal(scanPaths)
	if err != nil {
		return err
	}
	s.CustomScanPathsJSON = string(b)

	rules := s.PathTranslations
	if rules == nil {
		rules = []TranslationRule{}
	}
	b, err = json.Marshal(rules)
	if err != nil {
		return err
	}
	s.PathTranslationsJSON = string(b)
	return nil
}

// EnsureDefaultSettings inserts a settings row (and matching empty
// cloud_config row) with sensible defaults if one doesn't already exist —
// a no-op on every startup after the first. deviceName defaults to the
// machine hostname, matching db.js's defaultState.
func (s *Store) EnsureDefaultSettings(dataDir, backupsDir string) error {
	var count int
	if err := s.db.Get(&count, `SELECT COUNT(*) FROM settings WHERE id = 1`); err != nil {
		return fmt.Errorf("check existing settings: %w", err)
	}
	if count > 0 {
		return nil
	}

	deviceName, err := os.Hostname()
	if err != nil || deviceName == "" {
		deviceName = "Unknown Device"
	}
	nodeID := "node_" + uuidNoHyphens()

	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO settings (
			id, device_name, node_id, device_type, port, data_dir, backups_dir,
			sync_backups_dir, custom_scan_paths, path_translations
		) VALUES (1, ?, ?, 'desktop', 8383, ?, ?, ?, '[]', '[]')`,
		deviceName, nodeID, dataDir, backupsDir, backupsDir,
	) // sync_interval/sync_on_watch and the rest take their column defaults
	if err != nil {
		return fmt.Errorf("insert default settings: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO cloud_config (id) VALUES (1)`); err != nil {
		return fmt.Errorf("insert default cloud_config: %w", err)
	}

	return tx.Commit()
}

// ImportSettings inserts a complete settings row (used only by the legacy
// JSON importer, which must preserve the existing nodeId rather than
// generate a fresh one the way EnsureDefaultSettings does).
func (s *Store) ImportSettings(settings Settings) error {
	if err := settings.marshalJSONColumns(); err != nil {
		return err
	}
	_, err := s.db.NamedExec(`
		INSERT INTO settings (
			id, device_name, node_id, device_type, port, sync_interval, sync_on_watch,
			data_dir, backups_dir, sync_backups_dir, auto_delete_backups, auto_delete_days,
			auto_sync_on_track, custom_scan_paths, path_translations, relay_url, sync_code,
			host_relay, relay_port, start_on_boot, speed_limit_kbps, ui_mode
		) VALUES (
			1, :device_name, :node_id, :device_type, :port, :sync_interval, :sync_on_watch,
			:data_dir, :backups_dir, :sync_backups_dir, :auto_delete_backups, :auto_delete_days,
			:auto_sync_on_track, :custom_scan_paths, :path_translations, :relay_url, :sync_code,
			:host_relay, :relay_port, :start_on_boot, :speed_limit_kbps, :ui_mode
		)`, settings)
	if err != nil {
		return fmt.Errorf("import settings: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO cloud_config (id) VALUES (1)`); err != nil {
		return fmt.Errorf("insert cloud_config row: %w", err)
	}
	return nil
}

// GetSettings returns the current settings row.
func (s *Store) GetSettings() (Settings, error) {
	var settings Settings
	if err := s.db.Get(&settings, `SELECT * FROM settings WHERE id = 1`); err != nil {
		return Settings{}, fmt.Errorf("get settings: %w", err)
	}
	if err := settings.unmarshalJSONColumns(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// UpdateSettings persists the given settings as the new singleton row.
// NodeID is intentionally not updatable through this path — it is fixed at
// creation/import time since peers on other devices key pairing records by
// it.
func (s *Store) UpdateSettings(settings Settings) error {
	if err := settings.marshalJSONColumns(); err != nil {
		return err
	}
	_, err := s.db.NamedExec(`
		UPDATE settings SET
			device_name = :device_name,
			device_type = :device_type,
			port = :port,
			sync_interval = :sync_interval,
			sync_on_watch = :sync_on_watch,
			data_dir = :data_dir,
			backups_dir = :backups_dir,
			sync_backups_dir = :sync_backups_dir,
			auto_delete_backups = :auto_delete_backups,
			auto_delete_days = :auto_delete_days,
			auto_sync_on_track = :auto_sync_on_track,
			custom_scan_paths = :custom_scan_paths,
			path_translations = :path_translations,
			relay_url = :relay_url,
			sync_code = :sync_code,
			host_relay = :host_relay,
			relay_port = :relay_port,
			start_on_boot = :start_on_boot,
			speed_limit_kbps = :speed_limit_kbps,
			ui_mode = :ui_mode,
			default_max_snapshots = :default_max_snapshots,
			updated_at = datetime('now')
		WHERE id = 1`, settings)
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	return nil
}

func uuidNoHyphens() string {
	id := uuid.New().String()
	out := make([]byte, 0, len(id))
	for _, c := range id {
		if c != '-' {
			out = append(out, byte(c))
		}
	}
	return string(out)
}
