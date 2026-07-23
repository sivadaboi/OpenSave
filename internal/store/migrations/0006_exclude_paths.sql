-- Folders the auto-scanner must skip. Stored as a JSON array of absolute
-- paths; any discovered save at or under one of these is dropped from the
-- scan results. Lets users banish stale locations they don't want offered
-- again — e.g. an old "GSE saves" directory left behind after moving games
-- into Steam.
ALTER TABLE settings ADD COLUMN exclude_paths TEXT NOT NULL DEFAULT '[]';
