-- Cloud mirroring is now on by default. Uploads only actually happen once a
-- provider is configured/signed in, so this is safe for fresh setups; it
-- also flips existing installs that never touched the toggle (the row is
-- seeded by settings-init with the schema default, which used to be 0).
UPDATE cloud_config SET enabled = 1 WHERE id = 1;
