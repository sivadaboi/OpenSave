-- Keep deliberate snapshots out of the automatic churn.
--
-- Retention kept the newest N snapshots per branch regardless of where they
-- came from. In a game that auto-saves often — Elden Ring, Dragonsword:
-- Awakening — the watcher fills that budget within minutes of play, so a
-- snapshot the user took on purpose ("before the boss") is evicted by the
-- machine's own routine backups. That makes the manual snapshot button
-- pointless exactly when it matters most.
--
-- Manual and automatic snapshots now get separate budgets. max_snapshots
-- keeps its name and now governs automatic ones only; manual ones get their
-- own limit, defaulting to 0 = keep forever, which matches the existing
-- convention elsewhere that a limit of 0 or below disables pruning.
--
-- Defaulting to "keep forever" rather than to some number is deliberate: the
-- failure being fixed is snapshots disappearing without the user asking, and
-- a default that still deletes them would only move the threshold.
ALTER TABLE games ADD COLUMN max_manual_snapshots INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN default_max_manual_snapshots INTEGER NOT NULL DEFAULT 0;
