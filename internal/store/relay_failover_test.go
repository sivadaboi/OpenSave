package store

import (
	"testing"
)

// The two relays 0015 moved between. Both are switched off now, and 0018 moves
// installs off them onto relay.opensave.org, so these name points in the
// history rather than anything in service — which is why neither is called
// "new" any more.
const (
	relayBefore0015 = "wss://opensave-relay.onrender.com"
	relayAfter0015  = "wss://open-save-backup-relay.onrender.com"
)

// readMigration returns the shipped migration text, so these tests exercise
// the file that actually ships rather than a copy of its SQL that could drift
// away from it.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatalf("read embedded migration %s: %v", name, err)
	}
	return string(b)
}

// What a fresh install lands on is asserted in relay_domain_test.go, against
// the address that ships today. It was checked here as well until 0018 moved
// the default off both of this file's relays, at which point the two tests
// contradicted each other by construction and only one of them could be
// describing the current build.

// An install that predates the change is carrying the suspended address as a
// stored value, and the migration is the only thing that reaches it.
func TestFailoverMigrationMovesInstallsOffTheDeadRelay(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}
	// Put the database back into the state a 2.2.1 install is in.
	if _, err := s.db.Exec(`UPDATE settings SET relay_url = ?`, relayBefore0015); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(readMigration(t, "0015_relay_url_failover.sql")); err != nil {
		t.Fatalf("apply failover migration: %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.RelayURL != relayAfter0015 {
		t.Errorf("after migration relay = %q, want %q", got.RelayURL, relayAfter0015)
	}
}

// The one that matters most. Somebody running their own relay has their own
// address in this column. An unconditional UPDATE would take it away and
// silently route their saves through a server they did not choose — a far worse
// outcome than the outage this migration exists to fix.
func TestFailoverMigrationLeavesSelfHostedRelaysAlone(t *testing.T) {
	for _, custom := range []string{
		"wss://relay.example.com",
		"ws://192.168.1.50:8386",
		"wss://opensave-relay.onrender.com.evil.example", // similar prefix, not equal
		"wss://opensave-relay.onrender.com/",             // trailing slash: a different value
		" wss://opensave-relay.onrender.com",             // whitespace: also not equal
	} {
		t.Run(custom, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE settings SET relay_url = ?`, custom); err != nil {
				t.Fatal(err)
			}

			if _, err := s.db.Exec(readMigration(t, "0015_relay_url_failover.sql")); err != nil {
				t.Fatalf("apply failover migration: %v", err)
			}

			got, err := s.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			if got.RelayURL != custom {
				t.Errorf("self-hosted relay was overwritten: got %q, want %q", got.RelayURL, custom)
			}
		})
	}
}

// Migrations are recorded by name and applied once, so a second Open must not
// re-run the failover and drag somebody who has since moved to their own relay
// back onto the public one.
func TestFailoverMigrationDoesNotReapplyOnReopen(t *testing.T) {
	dir := t.TempDir() + "/opensave.db"
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE settings SET relay_url = ?`, "wss://relay.example.com"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.RelayURL != "wss://relay.example.com" {
		t.Errorf("relay after reopen = %q, want the self-hosted one to survive", got.RelayURL)
	}
}
