-- Content-based conflict detection: record the manifest hash both sides
-- agreed on at the last true convergence (like a git merge-base). Conflict
-- detection compares content against this instead of relying on mtimes vs
-- wall-clock sync times, which had a clock-skew blind window.
ALTER TABLE game_peer_sync_state ADD COLUMN agreed_hash TEXT NOT NULL DEFAULT '';
