-- Cross-device save matching.
--
-- match_by_app_id: when on, a peer's game is linked to a local game that
-- shares the same Steam App ID even if their names (and therefore ids)
-- differ — e.g. a Steam copy on one PC and a portable copy on another.
-- Off by default so a cracked copy and a legit copy of the same title are
-- never merged unless the user opts in.
ALTER TABLE settings ADD COLUMN match_by_app_id INTEGER NOT NULL DEFAULT 0;

-- Explicit "these two are the same game" links. A peer game id (alias_id)
-- resolves to the local canonical game (game_id) during sync — the manual
-- counterpart to App ID matching, for games that have no App ID or that the
-- user wants to link by hand. Set up per device; a link is removed
-- automatically (ON DELETE CASCADE) when its canonical game is untracked.
CREATE TABLE game_aliases (
  alias_id      TEXT PRIMARY KEY,
  game_id       TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  created_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_game_aliases_game ON game_aliases(game_id);
