-- Snapshot of a merged game's identity, kept on its alias row so unlinking
-- can bring the game back as its own tracked entry (its save files on disk
-- were never touched). Empty on aliases created any other way.
ALTER TABLE game_aliases ADD COLUMN alias_name      TEXT NOT NULL DEFAULT '';
ALTER TABLE game_aliases ADD COLUMN alias_save_path TEXT NOT NULL DEFAULT '';
ALTER TABLE game_aliases ADD COLUMN alias_app_id    TEXT NOT NULL DEFAULT '';
