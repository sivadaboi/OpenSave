-- What each snapshot actually holds, one row per file.
--
-- The question this exists to answer is "is this exact content of this exact
-- file recoverable from some snapshot?" Until now that was inferred from
-- Game.LastManifestHash — a single whole-save value describing the most recent
-- automatic snapshot, which several subsystems had to keep current and none
-- of them fully did. A save with one edited config file read as entirely
-- uncaptured; a value left stale by a sync read as uncaptured forever.
--
-- These rows are written once, when the snapshot is created, and never
-- updated. That is the property that matters. A snapshot is a fact about a
-- moment, so nothing that happens afterwards can make this description of it
-- wrong — where a "current state" pointer is wrong the instant anybody forgets
-- to move it.
--
-- Snapshots taken before this existed have no rows here, deliberately, and no
-- placeholder rows either: absence already means "cannot show this was
-- captured", which is exactly the answer that makes callers fall back to
-- taking a snapshot. A row saying "unknown" would be a second spelling of the
-- same thing for every query to handle.
--
-- ON DELETE CASCADE because a file list outliving its snapshot would claim
-- contents are recoverable from an archive that is gone — the one wrong answer
-- with teeth, since it is what lets a caller skip protecting a file. Foreign
-- keys are enforced on this connection (see store.Open).
-- root is the save location the file belongs to: empty for the game's primary
-- folder, otherwise the location's name. It is part of the key rather than
-- being folded into path because a manifest keys each location's files
-- separately, so "saves/slot1.sav" in the primary folder and the same relative
-- path inside a config location are different files that must not answer for
-- each other. Empty-for-primary matches how the rest of the code spells it
-- (delta.PrimaryRoot), so a game with one folder reads exactly as it would
-- have without locations existing.
CREATE TABLE IF NOT EXISTS snapshot_files (
  snapshot_id TEXT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  root        TEXT NOT NULL DEFAULT '',
  path        TEXT NOT NULL,
  hash        TEXT NOT NULL,
  PRIMARY KEY (snapshot_id, root, path)
);

-- The lookup is by content, not by snapshot: "does any snapshot hold this
-- path, in this location, at this hash". Without this index that is a scan of
-- every file of every snapshot of the game.
CREATE INDEX IF NOT EXISTS idx_snapshot_files_content ON snapshot_files(root, path, hash);
