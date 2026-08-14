-- Move installs onto a relay address the project controls.
--
-- Third time of asking, and the last: the two addresses below are vendor
-- hostnames on free hosting, and both were switched off — the first for
-- exhausting its allowance, the second the same way a week later. A relay
-- holds connections open and never idles, so it spends a monthly allowance
-- meant for services that sleep. That was never going to hold, and no third
-- free tier would have held either.
--
-- relay.opensave.org is a name the project owns, pointed at a machine it
-- rents. Moving hosts from here is a DNS record rather than a release, which
-- is the entire point: the previous two moves each needed an emergency version
-- and a banner asking every user to retype a setting.
--
-- All three abandoned addresses are listed. 0015 already moved people from the
-- first to the second, but not everyone ran it before the second died — someone
-- upgrading from 2.2.1 straight to this release passes through both, and an
-- install that never opened in between is still sitting on the original.
--
-- The third is the one that is easy to miss. It was never a shipped default:
-- it went out in a banner on the website during the second outage, asking
-- people to type it into settings by hand. So it sits in this column looking
-- exactly like a deliberate choice, and every install that followed that
-- banner is now parked on a third free tier waiting to be switched off the
-- same way. Leaving it out would strand precisely the users who did what we
-- asked. Nobody self-hosting could hold that value — it is our instance, and
-- the only way to have it is to have copied it from us.
--
-- The WHERE clause is the whole point, as it was in 0015. Anyone running their
-- own relay has their own address in this column, and an unconditional UPDATE
-- would take it away and quietly route their saves through somebody else's
-- server. Only rows still holding one of the three abandoned addresses are
-- touched.
UPDATE settings
SET relay_url = 'wss://relay.opensave.org'
WHERE relay_url IN (
  'wss://open-save-backup-relay.onrender.com',
  'wss://opensave-relay.onrender.com',
  'wss://opensave-public.up.railway.app'
);
