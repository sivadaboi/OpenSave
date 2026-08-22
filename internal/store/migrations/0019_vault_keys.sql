-- The vault keyring: the keys a device can decrypt stored saves with.
--
-- The pairwise keys added in 0017 cannot serve a server that keeps anything.
-- A payload sealed with the A-to-B key is unreadable by C, so a server holding
-- one copy has no ciphertext it can hand to every device. A vault key is one
-- symmetric key shared by all of a user's devices, wrapped for each of them
-- under the pairwise key they already have — the server stores one opaque blob
-- and can read none of it.
--
-- This is a table rather than two more columns on settings because a device
-- holds every vault key it has ever known, not just the current one. Two
-- things replace a key, and neither may cost anyone a save:
--
--   * Two devices that each already have a vault pair up. One key has to seal
--     what comes next, but both sides already have history under theirs.
--   * A device is unpaired, and the key it holds should stop opening anything
--     sealed afterwards.
--
-- Both append an epoch and move settings.vault_current_key_id. Nothing is ever
-- deleted here, and no row is ever rewritten. Re-encrypting history instead
-- would need every blob present and every device online at once, and a failure
-- halfway through would leave saves that no key opens — which is the one
-- outcome this design exists to rule out. Rows accumulate slowly (a handful
-- over a device's life) so keeping them all costs nothing worth measuring.
--
-- key_id is derived from the key, not random, so two devices holding the same
-- key agree on its name without exchanging one. It travels in the clear beside
-- every stored blob and reveals nothing about the key it names.
CREATE TABLE IF NOT EXISTS vault_keys (
  key_id     TEXT PRIMARY KEY,
  key_b64    TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- Which vault this device belongs to, and which of its keys seals new data.
--
-- Both empty means no vault yet: a device that has never paired and never
-- pushed to a storing server does not need one, and generating it on first use
-- means an install upgrading into this gets one without a migration having to
-- invent a key that no other device has seen.
--
-- vault_id is bound into every sealed payload, so a merge that changes it must
-- not change how existing blobs are opened — the value stored beside a blob is
-- the one that opens it, never whatever this column happens to say today.
--
-- Key material sits here at the same trust level as device_private_key and the
-- OAuth refresh tokens: anything that can read this file can already read the
-- saves in the folders these keys protect.
ALTER TABLE settings ADD COLUMN vault_id             TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN vault_current_key_id TEXT NOT NULL DEFAULT '';
