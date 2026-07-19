-- Tombstones for games the user deliberately untracked. Without this, a
-- peer that still tracks the game keeps requesting its manifest, and
-- ensureManifestGame auto-re-creates it — the game "comes back" seconds
-- after untracking. A tombstone makes the untrack durable: auto-track
-- refuses these ids until the user explicitly re-tracks (which clears it).
CREATE TABLE IF NOT EXISTS untracked_games (
    game_id         TEXT PRIMARY KEY,
    untracked_at_ms INTEGER NOT NULL DEFAULT 0
);
