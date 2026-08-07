-- Additional save locations for a game.
--
-- Plenty of titles keep their save split across two places — the save data
-- under AppData and the settings under Documents, or an emulator's saves
-- beside its memory cards — and tracking those as two separate games means
-- two cards in the library, two conflicts, and two things to restore in step
-- with each other.
--
-- games.save_path stays the primary location and is unchanged: every
-- existing game is a one-root game and needs no migration. Rows here are the
-- EXTRA locations, so this table is empty for almost every library.
--
-- The name, not the path, is what travels between devices. A folder called
-- "config" on a PC and the same folder on a Steam Deck live at completely
-- different paths, so the peer matches on name and resolves its own path
-- locally — and where it has no path for a name, it syncs what it can and
-- says so, rather than inventing a location for someone's save data.
CREATE TABLE IF NOT EXISTS game_roots (
    game_id    TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    -- Stable identifier shared across devices. Lowercase, no slashes; the
    -- primary location is implicitly named "" and is not stored here.
    name       TEXT NOT NULL,
    -- This device's absolute path for that name. May be empty: a game synced
    -- from a peer knows the name before anyone has said where it lives here.
    path       TEXT NOT NULL DEFAULT '',
    -- Ordering for display only, so the list does not shuffle between reads.
    ordinal    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (game_id, name)
);

CREATE INDEX IF NOT EXISTS idx_game_roots_game ON game_roots(game_id);
