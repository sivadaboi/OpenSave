-- Move installs off the suspended public relay.
--
-- The default lives in 0001_init.sql as a column default, and the settings row
-- takes it because EnsureDefaultSettings omits relay_url from its INSERT.
-- Changing that default therefore only reaches installs that have not been
-- created yet; every existing database already holds the old address as a
-- value. This migration is what reaches those.
--
-- Numbered 0015 rather than 0012 on purpose: 0012-0014 are already taken on
-- the development branch, and migrations are tracked by name, so a number
-- past them applies cleanly here and stays in order after the branches meet.
--
-- The WHERE clause is the whole point. Anyone running their own relay has
-- their own address in this column, and an unconditional UPDATE would take it
-- away from them and quietly point their saves at somebody else's server.
-- Only rows still holding the old default are touched.
UPDATE settings
SET relay_url = 'wss://open-save-backup-relay.onrender.com'
WHERE relay_url = 'wss://opensave-relay.onrender.com';
