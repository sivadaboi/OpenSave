package presets

import (
	"strings"
	"testing"
)

func TestNormalizeGameName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Elden Ring", "elden ring"},
		// The case that started this: repack folders use separators.
		{"Mina.The.Howler", "mina the howler"},
		{"Mina_The_Howler", "mina the howler"},
		{"Mina-The-Howler", "mina the howler"},
		// Apostrophes drop without gluing the words together.
		{"Baldur's Gate 3", "baldurs gate 3"},
		{"Baldurs Gate 3", "baldurs gate 3"},
		{"Sid Meier's Civilization VI", "sid meiers civilization vi"},
		// Trademark marks and stray punctuation are noise.
		{"DOOM™ Eternal", "doom eternal"},
		{"Portal 2 ®", "portal 2"},
		// Runs of separators collapse; edges trim.
		{"  Dead   by...Daylight  ", "dead by daylight"},
		{"...", ""},
		{"", ""},
	} {
		if got := normalizeGameName(tc.in); got != tc.want {
			t.Errorf("normalizeGameName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Two spellings of one title must land on the same key, or the whole point of
// normalizing is lost.
func TestNormalizeGameNameAgreesAcrossSpellings(t *testing.T) {
	for _, pair := range [][2]string{
		{"Mina.The.Howler", "Mina The Howler"},
		{"Baldur's Gate 3", "Baldurs Gate 3"},
		{"DOOM™ Eternal", "doom eternal"},
		{"Half-Life 2", "Half Life 2"},
	} {
		if a, b := normalizeGameName(pair[0]), normalizeGameName(pair[1]); a != b {
			t.Errorf("%q and %q normalize differently: %q vs %q", pair[0], pair[1], a, b)
		}
	}
}

// The manifest index is the reason a title outside the small curated list can
// be recognised at all. If it comes back tiny, the embedded manifest failed to
// load and inference has quietly narrowed back to the curated names.
func TestManifestNameIndexIsPopulated(t *testing.T) {
	index := manifestNameIndex()
	if len(index) < 1000 {
		t.Fatalf("manifest name index holds %d entries — expected thousands; "+
			"the embedded Ludusavi index likely failed to load", len(index))
	}
	for key, appID := range index {
		if key == "" || appID == "" {
			t.Fatalf("index contains an empty key or App ID: %q -> %q", key, appID)
		}
		if key != normalizeGameName(key) {
			t.Fatalf("index key %q is not normalized, so lookups will miss it", key)
		}
		break
	}
}

// Inference has to keep answering for the curated names, and now also answer
// for titles that only the manifest knows.
func TestInferAppIDFromNameUsesManifest(t *testing.T) {
	index := nameToAppIDIndex()

	// Curated exact matches must not regress.
	if got := inferAppIDFromName("Elden Ring", index); got != "1245620" {
		t.Errorf("Elden Ring -> %q, want 1245620", got)
	}
	if got := inferAppIDFromName("ULTRAKILL", index); got != "1229490" {
		t.Errorf("ULTRAKILL -> %q, want 1229490", got)
	}

	// Punctuation differences now resolve rather than falling through.
	if got := inferAppIDFromName("elden.ring", index); got != "1245620" {
		t.Errorf("elden.ring -> %q, want 1245620 via normalization", got)
	}

	// A folder name that is nothing at all must stay unresolved: a wrong App
	// ID is worse than none, because App-ID matching acts on it.
	for _, junk := range []string{"New folder", "Data", "temp", "asdfqwerzxcv"} {
		if got := inferAppIDFromName(junk, index); got != "" {
			t.Errorf("%q resolved to App ID %q — a false match would let App-ID "+
				"matching merge two unrelated games", junk, got)
		}
	}
}

func BenchmarkInferAppIDFromName(b *testing.B) {
	index := nameToAppIDIndex()
	manifestNameIndex() // build once, outside the loop
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inferAppIDFromName("Mina.The.Howler", index)
	}
}

// Folder names lose their word boundaries constantly — repack folders, Unity
// company directories, anything a human typed without spaces. Matching those
// is the difference between a game's cover art and App ID appearing on its
// own and the user being told nothing could be worked out.
func TestInferAppIDFromNameMatchesSquashedNames(t *testing.T) {
	index := nameToAppIDIndex()

	for _, tc := range []struct{ name, want string }{
		{"Mina the Hollower", "1875580"},
		{"Mina.the.Hollower", "1875580"},
		{"MinaTheHollower", "1875580"},
		{"MINATHEHOLLOWER", "1875580"},
		{"ELDENRING", "1245620"},
		{"hollowknight", "367520"},
		{"ANIMALWELL", "813230"},
	} {
		if got := inferAppIDFromName(tc.name, index); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.name, got, tc.want)
		}
	}

	// Compaction removes information, so it must not start matching things
	// that are merely short or generic. A wrong App ID is worse than none:
	// App-ID matching acts on it and would merge two unrelated saves.
	for _, junk := range []string{
		"New folder", "Data", "Saves", "temp", "backup", "save1",
		"Mina the Howler", // a real title misspelt is not a match
	} {
		if got := inferAppIDFromName(junk, index); got != "" {
			t.Errorf("%q resolved to %q — compaction is matching too loosely", junk, got)
		}
	}
}

// The compact index must actually be built, and must drop names that collide
// once their spaces are gone rather than picking a winner.
func TestManifestCompactIndexIsBuiltAndDeduped(t *testing.T) {
	full, compact := manifestNameIndex(), manifestCompactIndex()
	if len(compact) < 1000 {
		t.Fatalf("compact index holds %d entries — expected thousands", len(compact))
	}
	if len(compact) > len(full) {
		t.Errorf("compact index (%d) is larger than the index it derives from (%d)", len(compact), len(full))
	}
	for key := range compact {
		if strings.Contains(key, " ") {
			t.Fatalf("compact index key %q still contains a space", key)
		}
		break
	}
}
