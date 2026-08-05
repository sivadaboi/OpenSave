package version

import "testing"

// Ordering within a pre-release series, which is what decides whether someone
// running a beta is ever offered the next one.
//
// The suffixes used to be compared as plain strings. That is right up to
// beta.9 and wrong immediately after: "beta.10" sorts below "beta.9" because
// "1" precedes "9", so every beta.9 user would have been told they were up to
// date for as long as the series ran.
func TestComparePreReleaseSeries(t *testing.T) {
	newer := [][2]string{
		{"2.2.1-beta.2", "2.2.1-beta.1"},
		{"2.2.1-beta.10", "2.2.1-beta.9"},
		{"2.2.1-beta.10", "2.2.1-beta.2"},
		{"2.2.1-beta.100", "2.2.1-beta.99"},
		// A release outranks any pre-release of the same version.
		{"2.2.1", "2.2.1-beta.9"},
		// A newer core wins regardless of suffix.
		{"2.2.2-beta.1", "2.2.1"},
		{"2.3.0-beta.1", "2.2.1-beta.99"},
		// Fewer identifiers sort lower: "beta" precedes "beta.1".
		{"2.2.1-beta.1", "2.2.1-beta"},
		// rc follows beta alphabetically, which is the convention these names
		// are chosen to rely on.
		{"2.2.1-rc.1", "2.2.1-beta.9"},
	}
	for _, c := range newer {
		if got := Compare(c[0], c[1]); got != 1 {
			t.Errorf("Compare(%q, %q) = %d, want 1 (the first is newer)", c[0], c[1], got)
		}
		if got := Compare(c[1], c[0]); got != -1 {
			t.Errorf("Compare(%q, %q) = %d, want -1 (the first is older)", c[1], c[0], got)
		}
	}

	same := [][2]string{
		{"2.2.1-beta.1", "2.2.1-beta.1"},
		{"2.2.1", "2.2.1"},
	}
	for _, c := range same {
		if got := Compare(c[0], c[1]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", c[0], c[1], got)
		}
	}
}

// IsPreRelease is what decides whether someone is on the beta channel at all,
// so it has to agree with the tags the release workflow actually produces —
// it marks a release as a pre-release when the tag contains a hyphen.
func TestIsPreRelease(t *testing.T) {
	for _, v := range []string{"2.2.1-beta.1", "v2.2.1-beta.1", "2.3.0-rc.2", "1.0.0-alpha"} {
		if !IsPreRelease(v) {
			t.Errorf("IsPreRelease(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"2.2.1", "v2.2.1", "", "2.2"} {
		if IsPreRelease(v) {
			t.Errorf("IsPreRelease(%q) = true, want false", v)
		}
	}
}
