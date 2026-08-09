package presets

import (
	"path/filepath"
	"testing"
	"time"
)

// mk builds a measured save. Age is "days ago", so the freshest is the
// smallest number.
func mk(id, name, appID, path string, files int, daysAgo int) DiscoveredSave {
	d := DiscoveredSave{
		ID: id, Name: name, AppID: appID, SavePath: path, Type: "game",
		Measured: true, FileCount: files, TotalBytes: int64(files) * 100,
	}
	if files > 0 {
		d.LatestMtime = time.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour).Unix()
	}
	return d
}

func rolesByID(saves []DiscoveredSave) map[string]string {
	out := map[string]string{}
	for _, s := range saves {
		out[s.ID] = s.Role
	}
	return out
}

// A folder inside another of the same game's folders is the same files seen
// twice. It must never be offered as a location: the roots API refuses a
// path inside the primary, precisely because two locations covering one set
// of files fight over them.
func TestGroup_NestedFolderIsMarkedInside(t *testing.T) {
	base := filepath.Join("C:", "LocalLow", "Endnight", "SonsOfTheForest")
	saves := []DiscoveredSave{
		mk("outer", "Sons Of The Forest", "1326470", base, 54, 2),
		mk("inner", "Sons Of The Forest", "1326470", filepath.Join(base, "Saves"), 51, 2),
	}
	Group(saves)

	got := rolesByID(saves)
	if got["outer"] != RolePrimary || got["inner"] != RoleInside {
		t.Fatalf("roles = %v, want outer primary / inner inside", got)
	}
	if saves[0].GroupID != saves[1].GroupID {
		t.Error("the two folders of one game landed in different groups")
	}
}

// A chain must mark every folder below the top, not just the first one down.
func TestGroup_NestedChainMarksEveryLevel(t *testing.T) {
	a := filepath.Join("C:", "Games", "Title")
	saves := []DiscoveredSave{
		mk("a", "Title", "1", a, 10, 1),
		mk("b", "Title", "1", filepath.Join(a, "Saved"), 9, 1),
		mk("c", "Title", "1", filepath.Join(a, "Saved", "SaveGames"), 8, 1),
	}
	Group(saves)

	got := rolesByID(saves)
	if got["a"] != RolePrimary || got["b"] != RoleInside || got["c"] != RoleInside {
		t.Fatalf("roles = %v, want a primary and both descendants inside", got)
	}
}

// Folders sitting beside each other under one parent are the split-save case
// extra locations exist for — TrackMania's Scores, Tracks and Profiles.
func TestGroup_SiblingFoldersBecomeLocations(t *testing.T) {
	parent := filepath.Join("C:", "Documents", "TrackMania")
	saves := []DiscoveredSave{
		mk("scores", "Trackmania United Forever (Scores)", "7200", filepath.Join(parent, "Scores"), 1, 5),
		mk("tracks", "Trackmania United Forever (Tracks)", "7200", filepath.Join(parent, "Tracks"), 7, 1),
		mk("profiles", "Trackmania United Forever (Profiles)", "7200", filepath.Join(parent, "Profiles"), 1, 5),
	}
	Group(saves)

	got := rolesByID(saves)
	if got["tracks"] != RolePrimary {
		t.Errorf("the freshest sibling should lead the group: %v", got)
	}
	if got["scores"] != RoleLocation || got["profiles"] != RoleLocation {
		t.Errorf("siblings of the primary should be offered as locations: %v", got)
	}
}

// The same game found in unrelated places is usually one live folder and some
// leftovers. They are alternatives, never locations — offering them together
// would sync a dead folder alongside the real one.
func TestGroup_UnrelatedFoldersAreAlternativesNotLocations(t *testing.T) {
	saves := []DiscoveredSave{
		mk("rune", "Balatro", "2379780", filepath.Join("C:", "Public", "RUNE", "2379780"), 1, 800),
		mk("roaming", "Balatro", "2379780", filepath.Join("C:", "Roaming", "Balatro"), 7, 3),
		mk("gse", "Balatro", "2379780", filepath.Join("C:", "Roaming", "GSE Saves", "2379780"), 1, 400),
	}
	Group(saves)

	got := rolesByID(saves)
	if got["roaming"] != RolePrimary {
		t.Errorf("the most recently written folder should be the primary: %v", got)
	}
	for _, id := range []string{"rune", "gse"} {
		if got[id] != RoleAlternative {
			t.Errorf("%s = %q, want alternative — a folder elsewhere is not part of the same save", id, got[id])
		}
	}
}

// An empty folder must never be chosen as the folder to track, however
// recently its directory entry was touched.
func TestGroup_EmptyFolderNeverBecomesPrimary(t *testing.T) {
	empty := mk("empty", "Game", "42", filepath.Join("C:", "a", "Game"), 0, 0)
	empty.LatestMtime = 0
	saves := []DiscoveredSave{
		empty,
		mk("real", "Game", "42", filepath.Join("C:", "b", "Game"), 6, 500),
	}
	Group(saves)

	got := rolesByID(saves)
	if got["real"] != RolePrimary {
		t.Fatalf("an empty folder won the primary slot over one holding files: %v", got)
	}
}

// A folder that could not be measured is not known to be empty, so it must
// still be able to lead a group — the alternative is dropping a real save
// because a stat failed.
func TestGroup_UnmeasuredFolderCanStillBePrimary(t *testing.T) {
	// Both look identical on every other signal: no files, no write time. The
	// paths are chosen so alphabetical order — the last tiebreak — favours the
	// EMPTY one, leaving "measured and empty loses" as the only rule that can
	// produce the right answer. Ordered the other way this test passed with
	// that rule deleted.
	unmeasured := DiscoveredSave{ID: "unknown", Name: "Game", AppID: "42", SavePath: filepath.Join("C:", "z", "Game")}
	known := mk("known", "Game", "42", filepath.Join("C:", "a", "Game"), 0, 0)
	known.LatestMtime = 0 // measured, and empty

	saves := []DiscoveredSave{unmeasured, known}
	Group(saves)
	if got := rolesByID(saves); got["unknown"] != RolePrimary {
		t.Fatalf("an unmeasured folder lost to one known to be empty: %v", got)
	}
}

// An AppID is authoritative: the same game is named differently by different
// detection routes, and those rows still have to meet.
func TestGroup_AppIDGroupsAcrossDifferentNames(t *testing.T) {
	saves := []DiscoveredSave{
		mk("a", "Steam User 123 - AppID: 730", "730", filepath.Join("C:", "a"), 3, 1),
		mk("b", "Counter-Strike 2", "730", filepath.Join("C:", "b"), 3, 2),
	}
	Group(saves)
	if saves[0].GroupID != saves[1].GroupID {
		t.Error("two rows with the same AppID were not grouped")
	}
}

// The qualifier that keeps a Proton prefix's candidates distinguishable is
// exactly what must be ignored when deciding they are one game.
func TestGroup_TrailingQualifierIsIgnoredForGrouping(t *testing.T) {
	saves := []DiscoveredSave{
		mk("a", "Persona 3 Reload (SEGA)", "", filepath.Join("C:", "pfx", "SEGA"), 2, 1),
		mk("b", "Persona 3 Reload (Steam)", "", filepath.Join("C:", "pfx", "Steam"), 2, 1),
	}
	Group(saves)
	if saves[0].GroupID != saves[1].GroupID {
		t.Errorf("a trailing qualifier split one game into two groups: %q vs %q", saves[0].GroupID, saves[1].GroupID)
	}
}

// Two folders both called "Saves" are not evidence of one game. Grouping on a
// name that could belong to anything would merge unrelated titles, and that is
// the one mistake here that ends with someone's save in a folder they never
// chose.
func TestGroup_GenericNamesNeverGroup(t *testing.T) {
	for _, name := range []string{"Saves", "SaveData", "profiles", "User Data"} {
		saves := []DiscoveredSave{
			mk("a", name, "", filepath.Join("C:", "GameOne", "Saves"), 2, 1),
			mk("b", name, "", filepath.Join("D:", "GameTwo", "Saves"), 2, 1),
		}
		Group(saves)
		if saves[0].GroupID == saves[1].GroupID {
			t.Errorf("%q grouped two unrelated folders together", name)
		}
		if saves[0].Role != RoleOnly || saves[1].Role != RoleOnly {
			t.Errorf("%q: ungroupable rows should stand alone, got %v", name, rolesByID(saves))
		}
	}
}

func TestGroup_SingleResultIsItsOwnGroup(t *testing.T) {
	saves := []DiscoveredSave{mk("a", "Hades", "1145360", filepath.Join("C:", "Hades"), 4, 1)}
	Group(saves)
	if saves[0].Role != RoleOnly {
		t.Errorf("Role = %q, want %q", saves[0].Role, RoleOnly)
	}
	if saves[0].GroupID == "" {
		t.Error("even a lone result needs a group id, so the client can treat every row alike")
	}
}

// The scanner's ordering is an accident of which pass ran first. Which folder
// leads a group must not depend on it, or the suggestion changes between two
// scans of an unchanged machine.
func TestGroup_ResultDoesNotDependOnScanOrder(t *testing.T) {
	build := func() []DiscoveredSave {
		parent := filepath.Join("C:", "Documents", "Game")
		return []DiscoveredSave{
			mk("one", "Game", "9", filepath.Join(parent, "One"), 3, 9),
			mk("two", "Game", "9", filepath.Join(parent, "Two"), 3, 9),
			mk("three", "Game", "9", filepath.Join("D:", "Elsewhere", "Game"), 3, 9),
		}
	}
	forward := build()
	Group(forward)

	reversed := build()
	reversed[0], reversed[2] = reversed[2], reversed[0]
	Group(reversed)

	a, b := rolesByID(forward), rolesByID(reversed)
	for id := range a {
		if a[id] != b[id] {
			t.Errorf("%s: role %q forwards but %q reversed — the pick depends on scan order", id, a[id], b[id])
		}
	}
}

// Groups returns each game's folders together, the one to track first.
func TestGroups_SplitsAndOrdersWithinAGroup(t *testing.T) {
	parent := filepath.Join("C:", "Documents", "TM")
	saves := []DiscoveredSave{
		mk("inside", "TM", "7", filepath.Join(parent, "Tracks", "deep"), 1, 1),
		mk("scores", "TM", "7", filepath.Join(parent, "Scores"), 1, 5),
		mk("tracks", "TM", "7", filepath.Join(parent, "Tracks"), 7, 1),
		mk("other", "Hades", "1145360", filepath.Join("C:", "Hades"), 4, 1),
	}
	Group(saves)
	groups := Groups(saves)

	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	tm := groups[0]
	if len(tm) != 3 {
		t.Fatalf("the TrackMania group has %d folders, want 3", len(tm))
	}
	if tm[0].Role != RolePrimary {
		t.Errorf("a group must lead with the folder to track, got %q", tm[0].Role)
	}
	if tm[len(tm)-1].Role != RoleInside {
		t.Errorf("already-covered folders belong last, got %q", tm[len(tm)-1].Role)
	}
}
