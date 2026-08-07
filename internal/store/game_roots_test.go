package store

import "testing"

func rootsTestStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "elden-ring", Name: "Elden Ring", SavePath: `C:\Saves\ER`}); err != nil {
		t.Fatalf("CreateGame() error = %v", err)
	}
	return s
}

// Every existing game is a one-root game, and stays one after the migration.
func TestGamesStartWithNoExtraRoots(t *testing.T) {
	s := rootsTestStore(t)
	roots, err := s.ListGameRoots("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("a freshly tracked game has %d extra roots, want 0", len(roots))
	}
}

func TestAddAndListRoots(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", `C:\Users\me\Documents\ER`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameRoot("elden-ring", "mods", `D:\Mods\ER`); err != nil {
		t.Fatal(err)
	}

	roots, err := s.ListGameRoots("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2", len(roots))
	}
	// Ordinal ordering, so the list does not shuffle between reads.
	if roots[0].Name != "config" || roots[1].Name != "mods" {
		t.Errorf("roots came back as %q, %q — want them in insertion order", roots[0].Name, roots[1].Name)
	}
}

// The same name given twice is the same location, not a second one: this is
// how a root learned from a peer later gets a local path attached.
func TestAddingAKnownNameSetsItsPath(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", ""); err != nil {
		t.Fatal(err)
	}
	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 1 || roots[0].Mapped() {
		t.Fatalf("a root learned from a peer should exist but be unmapped, got %+v", roots)
	}

	if err := s.AddGameRoot("elden-ring", "config", `C:\Docs\ER`); err != nil {
		t.Fatal(err)
	}
	roots, _ = s.ListGameRoots("elden-ring")
	if len(roots) != 1 {
		t.Fatalf("naming a known root again made %d roots, want 1", len(roots))
	}
	if roots[0].Path != `C:\Docs\ER` {
		t.Errorf("path = %q, want the one just supplied", roots[0].Path)
	}
}

// Names are matched between devices, so they have to survive being typed by
// two different people. Anything that normalizes to the same identifier is
// the same root.
func TestRootNamesNormalizeSoDevicesAgree(t *testing.T) {
	cases := map[string]string{
		"config":    "config",
		"Config":    "config",
		"  CONFIG ": "config",
		"my/config": "my-config",
		`my\config`: "my-config",
		"/config/":  "config",
	}
	for in, want := range cases {
		if got := NormalizeRootName(in); got != want {
			t.Errorf("NormalizeRootName(%q) = %q, want %q", in, got, want)
		}
	}

	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "Config", `C:\A`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameRoot("elden-ring", " config ", `C:\B`); err != nil {
		t.Fatal(err)
	}
	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 1 {
		t.Fatalf("two spellings of one name made %d roots, want 1", len(roots))
	}
	if roots[0].Path != `C:\B` {
		t.Errorf("path = %q, want the second spelling to have updated the first", roots[0].Path)
	}
}

// The primary location is Game.SavePath and is never a row here — otherwise
// it could be removed, leaving a game with no save at all.
func TestPrimaryLocationCannotBeAddedAsARoot(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "", `C:\Saves\ER`); err == nil {
		t.Error("an unnamed root was accepted; the empty name is reserved for the primary location")
	}
	if err := s.AddGameRoot("elden-ring", "   ", `C:\Saves\ER`); err == nil {
		t.Error("a whitespace-only name was accepted")
	}
}

// Syncing iterates mapped roots only. A root whose local path is unknown must
// never appear, or a caller would join file paths onto an empty string and
// write a save into the working directory.
func TestUnmappedRootsAreNotOfferedForSync(t *testing.T) {
	s := rootsTestStore(t)
	_ = s.AddGameRoot("elden-ring", "config", `C:\Docs\ER`)
	_ = s.AddGameRoot("elden-ring", "mods", "") // known from a peer, not here

	paths, err := s.GameRootPaths("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("GameRootPaths returned %d entries, want only the mapped one: %+v", len(paths), paths)
	}
	if paths["config"] != `C:\Docs\ER` {
		t.Errorf("config = %q, want its local path", paths["config"])
	}
	if _, ok := paths["mods"]; ok {
		t.Error("an unmapped root was offered for sync")
	}
}

func TestRemoveGameRoot(t *testing.T) {
	s := rootsTestStore(t)
	_ = s.AddGameRoot("elden-ring", "config", `C:\Docs\ER`)
	if err := s.RemoveGameRoot("elden-ring", "Config"); err != nil {
		t.Fatal(err)
	}
	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 0 {
		t.Errorf("root survived removal: %+v — removal must normalize the name too", roots)
	}
}

// Untracking a game must not leave its locations behind to be inherited by
// the next game that happens to reuse the id.
func TestRootsGoWhenTheGameDoes(t *testing.T) {
	s := rootsTestStore(t)
	_ = s.AddGameRoot("elden-ring", "config", `C:\Docs\ER`)
	if err := s.DeleteGame("elden-ring"); err != nil {
		t.Fatal(err)
	}
	roots, err := s.ListGameRoots("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("deleting the game left %d orphaned roots", len(roots))
	}
}
