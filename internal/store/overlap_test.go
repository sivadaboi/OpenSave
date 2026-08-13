package store

import "testing"

// overlaps decides whether two save locations own the same files. It has to
// give the same answer everywhere: the old implementation used
// os.PathSeparator and filepath, so a Windows path was only understood on
// Windows, and on a Linux runner every one of these overlaps was missed and
// the guard passed everything through.
//
// These cases run identically on both platforms by construction — no t.TempDir,
// no filepath.Join — because a guard that only works on the developer's own
// operating system is the defect being fixed.
func TestOverlaps_SameAnswerOnAnyOperatingSystem(t *testing.T) {
	overlapping := []struct{ a, b, why string }{
		// Windows-shaped, which is what broke on Linux CI.
		{`C:\Saves\ER`, `C:\Saves\ER`, "identical"},
		{`C:\Saves\ER`, `C:\Saves\ER\config`, "b is inside a"},
		{`C:\Saves\ER\config`, `C:\Saves\ER`, "a is inside b"},
		{`C:\Saves\ER`, `C:\Saves`, "b contains a"},
		{`C:\Saves\ER`, `c:\saves\er`, "same tree, different case"},
		{`C:\Saves\ER`, `C:\Saves\ER\`, "trailing separator"},
		{`C:\Saves\ER`, `C:\Saves\ER\..\ER\config`, "dot-dot resolves back inside"},
		{`C:\Saves\ER`, `C:/Saves/ER/config`, "mixed separators, same place"},
		{`C:\`, `C:\Saves\ER`, "drive root contains everything on it"},

		// Unix-shaped, which is what real Linux and Deck installs use.
		{"/home/u/.local/share/game", "/home/u/.local/share/game", "identical"},
		{"/home/u/saves", "/home/u/saves/slot1", "b is inside a"},
		{"/home/u/saves/slot1", "/home/u", "a is deep inside b"},
		{"/", "/home/u/saves", "root contains everything"},
	}
	for _, c := range overlapping {
		if !overlaps(c.a, c.b) {
			t.Errorf("overlaps(%q, %q) = false, want true — %s", c.a, c.b, c.why)
		}
	}

	separate := []struct{ a, b, why string }{
		{`C:\Saves\ER`, `C:\Saves\Skyrim`, "siblings"},
		{`C:\Saves\ER`, `D:\Saves\ER`, "same path on another drive"},
		{`C:\Games\a`, `C:\Games\ab`, "a prefix that is not a parent folder"},
		{`C:\Games\ab`, `C:\Games\a`, "the same, the other way round"},
		{"/home/u/a", "/home/u/ab", "prefix but not a parent, Unix"},
		{"/home/u/saves", "/home/u/config", "siblings, Unix"},
		{`C:\Saves`, "/Saves", "a drive path and a Unix path are not the same place"},
	}
	for _, c := range separate {
		if overlaps(c.a, c.b) {
			t.Errorf("overlaps(%q, %q) = true, want false — %s", c.a, c.b, c.why)
		}
	}
}

// An unset path means "this device does not know where the root lives". It
// must never be treated as overlapping something, or the first unmapped root
// would block every later one.
func TestOverlaps_EmptyPathsNeverOverlap(t *testing.T) {
	for _, c := range [][2]string{
		{"", `C:\Saves\ER`},
		{`C:\Saves\ER`, ""},
		{"", ""},
		{"   ", `C:\Saves\ER`},
	} {
		if overlaps(c[0], c[1]) {
			t.Errorf("overlaps(%q, %q) = true; an unset path owns no files", c[0], c[1])
		}
	}
}

// The separator boundary is what stops "\Games\ab" reading as inside
// "\Games\a". Dropping it turns unrelated folders into refusals, which is the
// failure people would report as "it will not let me add my save folder".
func TestOverlaps_PrefixIsNotParenthood(t *testing.T) {
	if overlaps(`C:\Games\a`, `C:\Games\abc`) {
		t.Error(`C:\Games\abc was treated as inside C:\Games\a`)
	}
	if !overlaps(`C:\Games\a`, `C:\Games\a\bc`) {
		t.Error(`C:\Games\a\bc really is inside C:\Games\a`)
	}
}
