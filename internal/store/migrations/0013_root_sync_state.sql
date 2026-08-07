-- Merge-base lineage for a game's EXTRA save locations.
--
-- The primary location keeps using game_peer_sync_state, untouched. That
-- table holds the agreed_hash every device has already accumulated — the
-- value that, when it drifts, manufactures conflicts on saves nobody
-- touched. Re-keying it to add a root column would mean recreating it and
-- copying every row, which is a lot of risk to take on with the one piece of
-- state whose corruption is indistinguishable from data loss. Extra roots
-- are new, have no history to preserve, and can simply live here.
--
-- Each location keeps its own base, so they diverge independently: a config
-- folder that disagrees no longer blocks the save folder from syncing, and a
-- conflict names the location it is actually about.
CREATE TABLE IF NOT EXISTS game_root_sync_state (
    game_id     TEXT NOT NULL,
    peer_id     TEXT NOT NULL,
    -- Root NAME, not path. Never empty: the empty name means the primary
    -- location, which is not stored here.
    root        TEXT NOT NULL,
    agreed_hash TEXT NOT NULL DEFAULT '',
    -- Mirrors game_peer_sync_state.pushed_hash: the state handed to the peer
    -- whose receipt has not been confirmed yet, cleared the moment the two
    -- are known to agree on something newer.
    pushed_hash TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (game_id, peer_id, root)
);

CREATE INDEX IF NOT EXISTS idx_game_root_sync_peer ON game_root_sync_state(peer_id);
