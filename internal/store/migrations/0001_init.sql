CREATE TABLE settings (
  id                  INTEGER PRIMARY KEY CHECK (id = 1),
  device_name         TEXT NOT NULL,
  node_id             TEXT NOT NULL UNIQUE,
  device_type         TEXT NOT NULL DEFAULT 'desktop',
  port                INTEGER NOT NULL DEFAULT 8383,
  sync_interval       INTEGER NOT NULL DEFAULT 5000,
  sync_on_watch       INTEGER NOT NULL DEFAULT 1,
  data_dir            TEXT NOT NULL,
  backups_dir         TEXT NOT NULL,
  sync_backups_dir    TEXT NOT NULL,
  auto_delete_backups INTEGER NOT NULL DEFAULT 0,
  auto_delete_days    INTEGER NOT NULL DEFAULT 30,
  auto_sync_on_track  INTEGER NOT NULL DEFAULT 1,
  custom_scan_paths   TEXT NOT NULL DEFAULT '[]',
  path_translations   TEXT NOT NULL DEFAULT '[]',
  relay_url           TEXT NOT NULL DEFAULT 'wss://opensave-relay.onrender.com',
  sync_code           TEXT NOT NULL DEFAULT '',
  host_relay          INTEGER NOT NULL DEFAULT 0,
  relay_port          INTEGER NOT NULL DEFAULT 8386,
  start_on_boot       INTEGER NOT NULL DEFAULT 0,
  speed_limit_kbps    INTEGER NOT NULL DEFAULT 0,
  ui_mode             TEXT NOT NULL DEFAULT 'modern',
  updated_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE cloud_config (
  id                    INTEGER PRIMARY KEY CHECK (id = 1),
  enabled               INTEGER NOT NULL DEFAULT 0,
  provider              TEXT NOT NULL DEFAULT 'local',
  url                   TEXT NOT NULL DEFAULT '',
  username              TEXT NOT NULL DEFAULT '',
  password              TEXT NOT NULL DEFAULT '',
  headers_json          TEXT NOT NULL DEFAULT '{}',
  folder_id             TEXT NOT NULL DEFAULT '',
  custom_client_ids     TEXT NOT NULL DEFAULT '{}',
  custom_client_secrets TEXT NOT NULL DEFAULT '{}',
  access_token          TEXT NOT NULL DEFAULT '',
  refresh_token         TEXT NOT NULL DEFAULT '',
  expiry_time_ms        INTEGER NOT NULL DEFAULT 0,
  user_email            TEXT NOT NULL DEFAULT ''
);

CREATE TABLE games (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  save_path      TEXT NOT NULL,
  active_branch  TEXT NOT NULL DEFAULT 'main',
  auto_sync      INTEGER NOT NULL DEFAULT 1,
  max_snapshots  INTEGER NOT NULL DEFAULT 5,
  app_id         TEXT NOT NULL DEFAULT '',
  exe_path       TEXT NOT NULL DEFAULT '',
  cover_url      TEXT NOT NULL DEFAULT '',
  last_manifest_hash TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE branches (
  game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  PRIMARY KEY (game_id, name)
);

CREATE TABLE snapshots (
  id             TEXT PRIMARY KEY,
  game_id        TEXT NOT NULL,
  branch_name    TEXT NOT NULL,
  timestamp      TEXT NOT NULL,
  comment        TEXT NOT NULL DEFAULT '',
  is_system_auto INTEGER NOT NULL DEFAULT 0,
  zip_path       TEXT NOT NULL,
  size_bytes     INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY (game_id, branch_name) REFERENCES branches(game_id, name) ON DELETE CASCADE
);
CREATE INDEX idx_snapshots_game_branch ON snapshots(game_id, branch_name, timestamp);

CREATE TABLE peers (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  device_type  TEXT NOT NULL DEFAULT 'desktop',
  address      TEXT NOT NULL,
  port         INTEGER NOT NULL,
  paired_at    TEXT NOT NULL DEFAULT (datetime('now')),
  last_synced  TEXT,
  last_seen_ms INTEGER NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'offline'
);

CREATE TABLE game_peer_sync_state (
  game_id           TEXT NOT NULL,
  peer_id           TEXT NOT NULL,
  last_synced_files TEXT NOT NULL DEFAULT '[]',
  last_synced_dirs  TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (game_id, peer_id)
);
