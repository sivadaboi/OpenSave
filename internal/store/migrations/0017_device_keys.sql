-- Key material for end-to-end encryption between paired devices.
--
-- Two halves. This device's own long-lived X25519 key pair lives on the
-- settings row, and every peer's public half is pinned against that peer when
-- pairing completes. From the two, either side derives a key the other can
-- match and nothing in between can — not the network on a LAN, where sync is
-- plain HTTP, and not a relay, where the transport encryption ends at the
-- relay rather than at the far device.
--
-- All three default to empty, and empty means "not established". A device
-- upgrading into this has no keys until it generates them, and peers paired
-- before it have no key pinned until they pair again — so the code has to treat
-- an absent key as "cannot encrypt with this peer" and carry on in the clear,
-- exactly as it does today. There is no migration path that can invent a key
-- the other device has never seen.
--
-- The private key sits beside the OAuth refresh tokens already in this
-- database, at the same trust level: anything that can read the file can
-- already read every save it protects.
ALTER TABLE settings ADD COLUMN device_private_key TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN device_public_key  TEXT NOT NULL DEFAULT '';

-- Pinned on first pairing and not expected to change. A peer that presents a
-- different key later has either been reinstalled or is not the same device,
-- and those are indistinguishable from here — which is why the fingerprint is
-- shown to the person, who can tell the difference.
ALTER TABLE peers ADD COLUMN public_key TEXT NOT NULL DEFAULT '';
