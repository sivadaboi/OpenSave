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

// Two locations describing the same files do not merely duplicate work, they
// fight: the file appears in two manifests under two names, each sync patches
// it twice, and a deletion propagated for one is seen by the other as a file
// the peer is missing and pushed straight back.
func TestOverlappingLocationsAreRefused(t *testing.T) {
	s := rootsTestStore(t) // primary is C:\Saves\ER

	cases := []struct {
		name, path, why string
	}{
		{"same", `C:\Saves\ER`, "the primary save folder itself"},
		{"inside", `C:\Saves\ER\config`, "a folder inside the primary save"},
		{"parent", `C:\Saves`, "a folder containing the primary save"},
		{"case", `c:\saves\er`, "the primary save folder in different case"},
		{"trailing", `C:\Saves\ER\`, "the primary save folder with a trailing separator"},
	}
	for _, c := range cases {
		if err := s.AddGameRoot("elden-ring", c.name, c.path); err == nil {
			t.Errorf("%s (%s) was accepted as a separate location", c.path, c.why)
		}
	}

	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 0 {
		t.Errorf("%d overlapping locations were stored: %+v", len(roots), roots)
	}
}

// The same rule applies between two extra locations, not just against the
// primary one.
func TestLocationsCannotOverlapEachOther(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", `D:\Game\Config`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameRoot("elden-ring", "mods", `D:\Game\Config\mods`); err == nil {
		t.Error("a location inside another location was accepted")
	}
	if err := s.AddGameRoot("elden-ring", "saves", `D:\Game\Saves`); err != nil {
		t.Errorf("a genuinely separate location was refused: %v", err)
	}
}

// Re-pointing a location at a new path must not trip over its own old one.
func TestALocationCanBeMovedToANewPath(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", `D:\Game\Config`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameRoot("elden-ring", "config", `D:\Game\Config\v2`); err != nil {
		t.Errorf("moving a location to a path under its own old one was refused: %v", err)
	}
	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 1 || roots[0].Path != `D:\Game\Config\v2` {
		t.Errorf("after moving, roots = %+v", roots)
	}
}

// A location learned from a peer has no path yet, so there is nothing to
// overlap and it must still be recordable.
func TestUnmappedLocationSkipsTheOverlapCheck(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", ""); err != nil {
		t.Errorf("a location learned from a peer was refused: %v", err)
	}
}

// Root names are map keys, never path segments — but a name that looks like
// a traversal must not be able to become one if that ever changes.
func TestTraversalLikeNamesAreInert(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "..", `D:\Elsewhere`); err != nil {
		t.Fatal(err)
	}
	paths, err := s.GameRootPaths("elden-ring")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths[".."]; got != `D:\Elsewhere` {
		t.Errorf("name %q resolved to %q, want the configured path and nothing derived from the name", "..", got)
	}
}

// Learning that a location exists must never unset where it lives.
//
// An archive or a peer mentioning "config" arrives with a name and no path,
// because paths are local. If that blanked the folder the user had already
// chosen, the next sync and the next restore would both skip the location —
// and from where the user sits, their config files would simply stop being
// backed up, silently, because they imported a backup.
func TestLearningALocationNameDoesNotEraseItsPath(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.AddGameRoot("elden-ring", "config", `D:\Docs\ER`); err != nil {
		t.Fatal(err)
	}

	if err := s.NoteGameRoot("elden-ring", "config"); err != nil {
		t.Fatal(err)
	}

	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 1 {
		t.Fatalf("roots = %+v, want 1", roots)
	}
	if roots[0].Path != `D:\Docs\ER` {
		t.Errorf("path = %q after learning the name again; the configured folder was erased", roots[0].Path)
	}
	if !roots[0].Mapped() {
		t.Error("the location stopped being mapped, so syncing and restoring would now skip it")
	}
}

// A name not seen before is recorded unmapped, so the app can ask where it
// goes rather than the files quietly having nowhere to land.
func TestLearningANewLocationRecordsItUnmapped(t *testing.T) {
	s := rootsTestStore(t)
	if err := s.NoteGameRoot("elden-ring", "Config"); err != nil {
		t.Fatal(err)
	}
	roots, _ := s.ListGameRoots("elden-ring")
	if len(roots) != 1 || roots[0].Name != "config" {
		t.Fatalf("roots = %+v, want one normalized 'config'", roots)
	}
	if roots[0].Mapped() {
		t.Error("a location learned by name alone was recorded as mapped")
	}
}
