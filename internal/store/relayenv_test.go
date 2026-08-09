package store

import "testing"

// Pinning the relay by environment variable exists for machines that are
// provisioned rather than configured — a container, or an image rebuilt onto a
// fresh volume, where "run a command once after first boot" is not a step
// anyone gets to take.
func TestRelayURLEnv_OverridesTheStoredValue(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}

	before, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if before.RelayURLLocked {
		t.Fatal("nothing is pinned yet, so RelayURLLocked must be false")
	}
	stored := before.RelayURL

	t.Setenv(RelayURLEnv, "wss://relay.example.com")

	got, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.RelayURL != "wss://relay.example.com" {
		t.Errorf("RelayURL = %q, want the environment's value", got.RelayURL)
	}
	if !got.RelayURLLocked {
		t.Error("RelayURLLocked must say so, or the UI offers to edit a field it cannot change")
	}

	// Unsetting it must fall back to what was configured, not to whatever the
	// environment last said.
	t.Setenv(RelayURLEnv, "")
	after, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if after.RelayURL != stored {
		t.Errorf("after unsetting, RelayURL = %q, want the stored %q", after.RelayURL, stored)
	}
	if after.RelayURLLocked {
		t.Error("RelayURLLocked stayed true after the variable was removed")
	}
}

// The trap this guards: every caller does read-modify-write, and GetSettings
// hands back the override. Saving any unrelated setting would therefore burn
// the environment's value into the database — and then removing the variable
// would leave the machine pointed at a relay nobody ever configured, with
// nothing on screen to explain why.
func TestRelayURLEnv_NeverLeaksIntoTheDatabase(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}

	first, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	first.RelayURL = "wss://chosen-by-hand.example.com"
	if err := s.UpdateSettings(first); err != nil {
		t.Fatal(err)
	}

	t.Setenv(RelayURLEnv, "wss://from-the-environment.example.com")

	// An ordinary read-modify-write of something else entirely.
	live, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if live.RelayURL != "wss://from-the-environment.example.com" {
		t.Fatalf("the override did not apply: %q", live.RelayURL)
	}
	live.DeviceName = "Renamed"
	if err := s.UpdateSettings(live); err != nil {
		t.Fatal(err)
	}

	t.Setenv(RelayURLEnv, "")
	after, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if after.DeviceName != "Renamed" {
		t.Errorf("the unrelated change was lost: DeviceName = %q", after.DeviceName)
	}
	if after.RelayURL != "wss://chosen-by-hand.example.com" {
		t.Errorf("the environment's value was written to the database: RelayURL = %q, want the hand-set one", after.RelayURL)
	}
}

// Whitespace around a value pasted into a compose file or a systemd unit is
// easy to add and invisible once it breaks the URL.
func TestRelayURLEnv_TrimsAndIgnoresBlank(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatal(err)
	}
	base, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv(RelayURLEnv, "   ")
	blank, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if blank.RelayURLLocked || blank.RelayURL != base.RelayURL {
		t.Errorf("a whitespace-only value should be treated as unset, got %q (locked=%v)",
			blank.RelayURL, blank.RelayURLLocked)
	}

	t.Setenv(RelayURLEnv, "  wss://padded.example.com\n")
	padded, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if padded.RelayURL != "wss://padded.example.com" {
		t.Errorf("RelayURL = %q, want it trimmed", padded.RelayURL)
	}
}
