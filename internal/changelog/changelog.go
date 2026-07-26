// Package changelog turns CHANGELOG.md into structured releases.
//
// The file is written for GitHub, so the app used to show it by dumping the
// raw markdown into a scrollable box: users read literal "##" and "**" and a
// wall of undifferentiated text. Parsing it once here lets the UI render
// versions, dates and sections properly, and lets "what changed since the
// version you were on" be answered rather than approximated.
package changelog

import (
	"strings"

	"github.com/opensave/opensave/internal/version"
)

// Entry is one bullet: a lead-in (the bolded summary a line often starts
// with) and the rest of the text. Splitting them lets the UI emphasise the
// summary without interpreting markdown at render time.
type Entry struct {
	Lead string `json:"lead,omitempty"`
	Text string `json:"text"`
}

// Section is a "### Fixed" / "### Added" group within a release.
type Section struct {
	Title   string  `json:"title"`
	Entries []Entry `json:"entries"`
}

// Release is one "## [2.2.0] — 2026-07-26" block.
type Release struct {
	Version  string    `json:"version"`
	Date     string    `json:"date,omitempty"`
	Sections []Section `json:"sections"`
}

// Parse reads the changelog markdown into releases, newest first (the order
// the file itself uses). Anything before the first version heading — the
// title and preamble — is ignored.
func Parse(md string) []Release {
	var releases []Release
	var cur *Release
	var sec *Section

	flushSection := func() {
		if cur != nil && sec != nil && len(sec.Entries) > 0 {
			cur.Sections = append(cur.Sections, *sec)
		}
		sec = nil
	}
	flushRelease := func() {
		flushSection()
		if cur != nil {
			releases = append(releases, *cur)
		}
		cur = nil
	}

	// Bullets wrap across lines in this file, so a continuation is appended
	// to the entry above rather than dropped or made into its own bullet.
	var pending *Entry
	commitPending := func() {
		if pending == nil {
			return
		}
		lead, text := splitLead(strings.TrimSpace(pending.Text))
		if text != "" || lead != "" {
			sec.Entries = append(sec.Entries, Entry{Lead: lead, Text: text})
		}
		pending = nil
	}

	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "## "):
			commitPending()
			flushRelease()
			v, date := parseReleaseHeading(strings.TrimPrefix(trimmed, "## "))
			if v == "" {
				continue
			}
			cur = &Release{Version: v, Date: date}

		case strings.HasPrefix(trimmed, "### "):
			if cur == nil {
				continue
			}
			commitPending()
			flushSection()
			sec = &Section{Title: strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))}

		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			if cur == nil {
				continue
			}
			if sec == nil {
				// Bullets before any "###" — keep them under an unnamed group
				// rather than losing them.
				sec = &Section{}
			}
			commitPending()
			pending = &Entry{Text: strings.TrimSpace(trimmed[2:])}

		case trimmed == "":
			commitPending()

		default:
			// Wrapped continuation of the bullet above.
			if pending != nil {
				pending.Text += " " + trimmed
			}
		}
	}
	commitPending()
	flushRelease()
	return releases
}

// Since returns the releases newer than fromVersion — what to show someone
// who just updated. An unknown or empty fromVersion yields the newest release
// only, which is the right greeting for a fresh install: a first-run wall of
// every historical change is not a welcome.
func Since(releases []Release, fromVersion string) []Release {
	if fromVersion == "" {
		if len(releases) > 0 {
			return releases[:1]
		}
		return nil
	}
	var out []Release
	for _, r := range releases {
		if version.Compare(r.Version, fromVersion) > 0 {
			out = append(out, r)
		}
	}
	if len(out) == 0 && len(releases) > 0 {
		return releases[:1]
	}
	return out
}

// parseReleaseHeading pulls the version and date out of headings like
// "[2.2.0] — 2026-07-26", "[2.2.0] - 2026-07-26" or "2.2.0".
func parseReleaseHeading(h string) (ver, date string) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", ""
	}
	// Version: bracketed, or the first token.
	rest := h
	if i := strings.Index(h, "["); i >= 0 {
		if j := strings.Index(h[i:], "]"); j > 0 {
			ver = strings.TrimSpace(h[i+1 : i+j])
			rest = h[i+j+1:]
		}
	}
	if ver == "" {
		fields := strings.Fields(h)
		ver = strings.Trim(fields[0], "[]")
		rest = strings.TrimPrefix(h, fields[0])
	}
	// Anything after the separator is the date, when there is one.
	rest = strings.TrimSpace(rest)
	rest = strings.TrimLeft(rest, "—-– \t")
	return ver, strings.TrimSpace(rest)
}

// splitLead separates a leading "**bolded summary.**" from the rest, and
// strips the markdown emphasis markers either way — the UI styles these, so
// leaving the asterisks in would render them literally.
func splitLead(s string) (lead, text string) {
	if strings.HasPrefix(s, "**") {
		if end := strings.Index(s[2:], "**"); end >= 0 {
			lead = strings.TrimSpace(s[2 : 2+end])
			text = strings.TrimSpace(s[2+end+2:])
			return lead, stripEmphasis(text)
		}
	}
	return "", stripEmphasis(s)
}

// stripEmphasis removes bold/italic markers, leaving backticks alone so the
// UI can still render inline code distinctly.
func stripEmphasis(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	return s
}
