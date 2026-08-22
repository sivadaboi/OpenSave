package changelog

import (
	"strings"
	"testing"
)

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

// The prose under a version heading says what the release is about; the
// entries beneath are details of it. Dropping it left the app showing a wall
// of bullets with nothing tying them together.
func TestParseKeepsReleaseIntro(t *testing.T) {
	md := `# Changelog

Preamble that belongs to no release and must stay ignored.

## [2.3.0] — 2026-08-21

First paragraph that wraps
across two source lines.

Second paragraph.

### Fixed

- **A thing.** Details.

## [2.2.0] — 2026-08-01

Older release intro.

### Added

- **Another.** More.
`
	rels := Parse(md)
	if len(rels) != 2 {
		t.Fatalf("got %d releases, want 2", len(rels))
	}

	if len(rels[0].Intro) != 2 {
		t.Fatalf("2.3.0 intro = %#v, want 2 paragraphs", rels[0].Intro)
	}
	if want := "First paragraph that wraps across two source lines."; rels[0].Intro[0] != want {
		t.Errorf("wrapped intro line not joined:\n got %q\nwant %q", rels[0].Intro[0], want)
	}
	if rels[0].Intro[1] != "Second paragraph." {
		t.Errorf("second paragraph = %q", rels[0].Intro[1])
	}
	// The file preamble sits before any version heading and is not an intro.
	for _, p := range rels[0].Intro {
		if strings.Contains(p, "Preamble") {
			t.Error("the file preamble leaked into a release intro")
		}
	}
	if len(rels[1].Intro) != 1 || rels[1].Intro[0] != "Older release intro." {
		t.Errorf("2.2.0 intro = %#v", rels[1].Intro)
	}
	// Sections must be unaffected.
	if len(rels[0].Sections) != 1 || rels[0].Sections[0].Title != "Fixed" {
		t.Errorf("sections changed: %#v", rels[0].Sections)
	}
}

// Prose that wraps a bullet still belongs to that bullet, and prose after a
// section has started is not a second introduction.
func TestParseIntroStopsAtFirstSection(t *testing.T) {
	md := `## [1.0.0] — 2026-01-01

The intro.

### Fixed

- **Lead.** Body text
  wrapping onward.

  A second paragraph of the same bullet.
`
	rels := Parse(md)
	if len(rels) != 1 {
		t.Fatalf("got %d releases", len(rels))
	}
	if len(rels[0].Intro) != 1 || rels[0].Intro[0] != "The intro." {
		t.Fatalf("intro = %#v, want just the pre-section prose", rels[0].Intro)
	}
	for _, p := range rels[0].Intro {
		if strings.Contains(p, "second paragraph") {
			t.Error("prose inside a section was captured as release intro")
		}
	}
}

// A release with no prose must report none rather than an empty string, so
// the UI can tell "no intro" from "an empty one".
func TestParseNoIntroIsNil(t *testing.T) {
	rels := Parse("## [1.0.0] — 2026-01-01\n\n### Fixed\n\n- **A.** B.\n")
	if len(rels) != 1 {
		t.Fatalf("got %d releases", len(rels))
	}
	if rels[0].Intro != nil {
		t.Errorf("intro = %#v, want nil", rels[0].Intro)
	}
}
