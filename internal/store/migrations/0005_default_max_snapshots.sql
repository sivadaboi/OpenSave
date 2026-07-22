-- Global default for how many snapshots to keep per game/branch. New games
-- inherit it (instead of a hardcoded 5), and a Settings action can apply it
-- to every existing game at once. Retention was previously per-game only,
-- with no discoverable global control.
ALTER TABLE settings ADD COLUMN default_max_snapshots INTEGER NOT NULL DEFAULT 20;
