package ignore

import "testing"

func TestNoRulesExcludesNothing(t *testing.T) {
	r := Parse("")
	if !r.Empty() {
		t.Error("an empty list should report Empty so callers can skip the work")
	}
	for _, p := range []string{"Config.gs", "sub/Progress.gs", ""} {
		if r.Match(p) {
			t.Errorf("%q excluded with no rules", p)
		}
	}
}

// The case from the request: a game keeping its save and its device-specific
// config in one folder.
func TestExcludesOneNamedFile(t *testing.T) {
	r := Parse("Config.gs")
	for _, in := range []string{"Config.gs", "sub/Config.gs", "a/b/Config.gs"} {
		if !r.Match(in) {
			t.Errorf("%q should be excluded", in)
		}
	}
	for _, in := range []string{"Progress.gs", "Persistent.gs", "Config.gs.bak", "MyConfig.gs"} {
		if r.Match(in) {
			t.Errorf("%q must not be excluded by the rule \"Config.gs\"", in)
		}
	}
}

// Rules are written on one device and used on another. A pattern typed on
// Windows has to keep working on a Steam Deck, so matching ignores case
// everywhere rather than following the local filesystem.
func TestMatchingIgnoresCase(t *testing.T) {
	r := Parse("Config.gs")
	for _, in := range []string{"config.gs", "CONFIG.GS", "CoNfIg.Gs"} {
		if !r.Match(in) {
			t.Errorf("%q should match the rule regardless of case", in)
		}
	}
}

func TestCommentsAndBlankLines(t *testing.T) {
	r := Parse("# device-specific\n\n  Config.gs  \n\n# end\n")
	if !r.Match("Config.gs") {
		t.Error("the pattern between comments was not applied")
	}
	if r.Match("#") || r.Match("end") {
		t.Error("a comment was treated as a pattern")
	}
}

func TestWildcards(t *testing.T) {
	r := Parse("*.log")
	if !r.Match("debug.log") || !r.Match("logs/debug.log") {
		t.Error("*.log should match at any depth")
	}
	if r.Match("debug.log.sav") {
		t.Error("*.log matched a file that only contains .log")
	}
}

// A leading slash anchors to the save folder, so a rule can name the exact
// file it means without catching same-named files deeper in the tree.
func TestAnchoredPatterns(t *testing.T) {
	r := Parse("/Config.gs")
	if !r.Match("Config.gs") {
		t.Error("the anchored pattern did not match at the root")
	}
	if r.Match("profiles/Config.gs") {
		t.Error("an anchored pattern matched deeper in the tree")
	}
}

// A pattern containing a slash is anchored too, as in a .gitignore.
func TestPatternWithASlashIsAnchored(t *testing.T) {
	r := Parse("saves/Config.gs")
	if !r.Match("saves/Config.gs") {
		t.Error("did not match the path it names")
	}
	if r.Match("profiles/saves/Config.gs") {
		t.Error("a pattern with a slash matched somewhere other than the root")
	}
}

// A trailing slash means a folder, and a folder means everything inside it —
// otherwise "logs/" would exclude an empty directory and sync its contents.
func TestDirectoryPatternsCoverTheirContents(t *testing.T) {
	r := Parse("logs/")
	for _, in := range []string{"logs", "logs/today.txt", "logs/old/a.txt"} {
		if !r.Match(in) {
			t.Errorf("%q should be excluded by \"logs/\"", in)
		}
	}
	if r.Match("logs.txt") {
		t.Error("\"logs/\" matched a file named logs.txt")
	}
}

func TestDoubleStarCrossesDirectories(t *testing.T) {
	r := Parse("cache/**/tmp")
	if !r.Match("cache/a/tmp") || !r.Match("cache/a/b/c/tmp") {
		t.Error("** should cross any number of directories")
	}
	if r.Match("other/a/tmp") {
		t.Error("matched outside the anchored prefix")
	}
}

// Negation is what makes a broad rule usable: exclude everything of a kind,
// then keep the one that matters.
func TestNegationReIncludes(t *testing.T) {
	r := Parse("*.gs\n!Progress.gs")
	if !r.Match("Config.gs") {
		t.Error("Config.gs should still be excluded")
	}
	if r.Match("Progress.gs") {
		t.Error("Progress.gs was re-included by \"!\" and must sync")
	}
}

// Order decides, as in a .gitignore: the last rule that matches wins.
func TestLaterRulesWin(t *testing.T) {
	if Parse("!Config.gs\nConfig.gs").Match("Config.gs") != true {
		t.Error("a later exclusion should override an earlier re-inclusion")
	}
	if Parse("Config.gs\n!Config.gs").Match("Config.gs") != false {
		t.Error("a later re-inclusion should override an earlier exclusion")
	}
}

// Paths arrive from the manifest slash-separated, but a caller passing a
// Windows path must not silently get no matches.
func TestBackslashPathsStillMatch(t *testing.T) {
	r := Parse("Config.gs")
	if !r.Match(`sub\Config.gs`) {
		t.Error("a backslash-separated path did not match")
	}
}

// A rule that names a whole tree must not accidentally exclude the entire
// save because of a stray empty pattern.
func TestStrayPatternsAreIgnored(t *testing.T) {
	r := Parse("!\n/\n   \n")
	if !r.Empty() {
		t.Errorf("stray lines produced %d pattern(s); an empty or bare-slash line must not become a rule that hides the whole save", len(r.patterns))
	}
}
