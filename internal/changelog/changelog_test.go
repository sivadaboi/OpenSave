package changelog

import "testing"

const sample = `# Changelog

All notable changes are documented here.

## [2.2.0] — 2026-07-26

### Fixed

- **Games matched across devices could agree what to sync, then fail to
  sync it.** App-ID matching was applied in one place only.
- A second, plain entry with ` + "`inline code`" + ` kept intact.

### Changed

- **Large saves sync faster.** Blocks stream to disk.

## [2.1.1] — 2026-07-20

### Fixed

- Conflict resolutions now stick.
`

func TestParse(t *testing.T) {
	rel := Parse(sample)
	if len(rel) != 2 {
		t.Fatalf("got %d releases, want 2: %+v", len(rel), rel)
	}
	if rel[0].Version != "2.2.0" || rel[0].Date != "2026-07-26" {
		t.Errorf("first release = %q / %q, want 2.2.0 / 2026-07-26", rel[0].Version, rel[0].Date)
	}
	if len(rel[0].Sections) != 2 {
		t.Fatalf("want 2 sections, got %d: %+v", len(rel[0].Sections), rel[0].Sections)
	}
	if rel[0].Sections[0].Title != "Fixed" || rel[0].Sections[1].Title != "Changed" {
		t.Errorf("section titles = %q, %q", rel[0].Sections[0].Title, rel[0].Sections[1].Title)
	}

	fixed := rel[0].Sections[0].Entries
	if len(fixed) != 2 {
		t.Fatalf("want 2 entries under Fixed, got %d: %+v", len(fixed), fixed)
	}

	// A bullet wrapped over several lines is one entry, not three.
	if fixed[0].Lead != "Games matched across devices could agree what to sync, then fail to sync it." {
		t.Errorf("lead = %q — wrapped lines should rejoin", fixed[0].Lead)
	}
	if fixed[0].Text != "App-ID matching was applied in one place only." {
		t.Errorf("text = %q", fixed[0].Text)
	}

	// No markdown emphasis may survive into what the UI renders.
	for _, s := range rel[0].Sections {
		for _, e := range s.Entries {
			if containsAny(e.Lead, "**") || containsAny(e.Text, "**") {
				t.Errorf("emphasis markers left in %+v — they'd render literally", e)
			}
		}
	}
	// Inline code is left for the UI to style.
	if fixed[1].Text != "A second, plain entry with `inline code` kept intact." {
		t.Errorf("plain entry = %q", fixed[1].Text)
	}
	if fixed[1].Lead != "" {
		t.Errorf("entry without a bold lead-in should have no lead, got %q", fixed[1].Lead)
	}
}

func TestSince(t *testing.T) {
	rel := Parse(sample)

	got := Since(rel, "2.1.1")
	if len(got) != 1 || got[0].Version != "2.2.0" {
		t.Errorf("Since(2.1.1) = %+v, want just 2.2.0", got)
	}

	// Someone already current sees the newest release rather than nothing —
	// the greeting is reached only when something did change.
	if got := Since(rel, "2.2.0"); len(got) != 1 || got[0].Version != "2.2.0" {
		t.Errorf("Since(current) = %+v", got)
	}

	// A fresh install gets the newest release, not the whole history.
	if got := Since(rel, ""); len(got) != 1 || got[0].Version != "2.2.0" {
		t.Errorf("Since(\"\") = %+v, want only the newest", got)
	}

	// Coming from far behind shows everything since.
	if got := Since(rel, "2.0.0"); len(got) != 2 {
		t.Errorf("Since(2.0.0) = %d releases, want 2", len(got))
	}
}

func TestParseHandlesTheRealChangelogShape(t *testing.T) {
	// Guards the heading dialects the file has actually used.
	for _, h := range []string{
		"[2.2.0] — 2026-07-26",
		"[2.2.0] - 2026-07-26",
		"2.2.0",
	} {
		v, _ := parseReleaseHeading(h)
		if v != "2.2.0" {
			t.Errorf("parseReleaseHeading(%q) = %q, want 2.2.0", h, v)
		}
	}
}

func containsAny(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
