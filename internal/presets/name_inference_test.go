package presets

import (
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
