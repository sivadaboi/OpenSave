package store

import "testing"

const ownDomainRelay = "wss://relay.opensave.org"

// The vendor hostnames we have asked users to point at and then abandoned, in
// the order they died. The last was never a shipped default — it went out in a
// website banner during the second outage, so the installs holding it are the
// ones that did what we asked.
var abandonedRelays = []string{
	"wss://opensave-relay.onrender.com",
	"wss://open-save-backup-relay.onrender.com",
	"wss://opensave-public.up.railway.app",
}

// A fresh install must land on the relay the project controls.
//
// Not obvious from the changed default in 0001 alone: migrations all run
// inside Open, and the settings row is created afterwards by
// EnsureDefaultSettings, which omits relay_url and so takes the column
// default. An UPDATE migration by itself would run against an empty table.
func TestFreshInstallUsesOwnDomain(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.RelayURL != ownDomainRelay {
		t.Errorf("fresh install relay = %q, want %q", got.RelayURL, ownDomainRelay)
	}
}

// Every abandoned address has to be moved, not just the most recent one.
//
// 0015 moved people from the first to the second, but the second died a week
// later and not everyone ran 0015 in between. An install upgrading from 2.2.1
// passes through both in one go, and one that was never opened is still
// sitting on the original. The third came from a website banner rather than a
// release, so it is held by the users who followed our instructions during the
// outage — the last people who should be left behind.
func TestAbandonedRelaysAreMoved(t *testing.T) {
	for _, dead := range abandonedRelays {
		t.Run(dead, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE settings SET relay_url = ?`, dead); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(readMigration(t, "0018_relay_own_domain.sql")); err != nil {
				t.Fatalf("apply migration: %v", err)
			}
			got, err := s.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			if got.RelayURL != ownDomainRelay {
				t.Errorf("relay = %q, want %q — this install stays pointed at a dead server", got.RelayURL, ownDomainRelay)
			}
		})
	}
}

// The one that matters. Somebody running their own relay has their own address
// here, and taking it away would route their saves through a server they did
// not choose — a worse outcome than the outage this migration exists to fix.
func TestSelfHostedRelaysSurvive(t *testing.T) {
	for _, custom := range []string{
		"wss://relay.example.com",
		"ws://192.168.1.50:8386",
		"wss://relay.opensave.org.evil.example",            // lookalike prefix
		"wss://open-save-backup-relay.onrender.com/",       // trailing slash: a different value
		" wss://open-save-backup-relay.onrender.com",       // leading space: also different
		"wss://my-own-open-save-backup-relay.onrender.com", // contains the dead name
	} {
		t.Run(custom, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE settings SET relay_url = ?`, custom); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(readMigration(t, "0018_relay_own_domain.sql")); err != nil {
				t.Fatalf("apply migration: %v", err)
			}
			got, err := s.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			if got.RelayURL != custom {
				t.Errorf("a self-hosted relay was overwritten: got %q, want %q", got.RelayURL, custom)
			}
		})
	}
}

// 0016 and 0017 do not exist on this line: they are the 2.3.0 migrations, and
// this release carries only the relay change. The number decides ordering, not
// identity — migrations are recorded by name — so the gap costs nothing, and
// the file keeps the same name it has on the 2.3.0 line deliberately. An
// install that takes 2.2.3 and later upgrades has already recorded
// 0018_relay_own_domain.sql, so 2.3.0 will not run it a second time over a
// relay the user has chosen for themselves in the meantime.
func TestMigrationIsNamedToMatchTheNextRelease(t *testing.T) {
	if _, err := migrationsFS.ReadFile("migrations/0018_relay_own_domain.sql"); err != nil {
		t.Fatalf("the migration must ship under the same name as on 2.3.0: %v", err)
	}
}
