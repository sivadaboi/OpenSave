package selfupdate

import "testing"

// Which channel a build is on. The second case is the whole point: someone
// running a pre-release is offered the next one without having to find a
// setting first, because the alternative is sitting on a build whose update
// check says "up to date" until the final release overtakes it.
func TestWantsPreReleases(t *testing.T) {
	cases := []struct {
		channel, current string
		want             bool
	}{
		{"stable", "2.2.0", false},
		{"", "2.2.0", false},
		{"beta", "2.2.0", true},          // opted in while on a stable build
		{"BETA", "2.2.0", true},          // however it was written
		{"stable", "2.2.1-beta.1", true}, // already on a beta: never stranded
		{"", "2.2.1-beta.1", true},
	}
	for _, c := range cases {
		if got := WantsPreReleases(c.channel, c.current); got != c.want {
			t.Errorf("WantsPreReleases(%q, %q) = %v, want %v", c.channel, c.current, got, c.want)
		}
	}
}

// The beta channel picks the highest version, not the first in the list —
// GitHub returns releases newest-created first, which is not the same thing
// once a patch to an older line is published after a newer pre-release.
func TestNewestOfPicksHighestVersion(t *testing.T) {
	got, err := newestOf([]Release{
		{TagName: "v2.2.1-beta.2", Prerelease: true},
		{TagName: "v2.2.1-beta.10", Prerelease: true},
		{TagName: "v2.2.0"},
		{TagName: ""}, // no tag: ignored rather than sorted
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v2.2.1-beta.10" {
		t.Errorf("newest = %q, want v2.2.1-beta.10", got.TagName)
	}
}

// A beta must never be a one-way door: once the final release is out it is
// newer than every pre-release of that version, so it is what gets offered.
func TestNewestOfPrefersTheFinalRelease(t *testing.T) {
	got, err := newestOf([]Release{
		{TagName: "v2.2.1-beta.3", Prerelease: true},
		{TagName: "v2.2.1"},
		{TagName: "v2.2.1-beta.2", Prerelease: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v2.2.1" {
		t.Errorf("newest = %q, want the final v2.2.1", got.TagName)
	}
}

func TestNewestOfWithNothingPublished(t *testing.T) {
	if _, err := newestOf(nil); err == nil {
		t.Error("expected an error when there are no releases")
	}
	if _, err := newestOf([]Release{{TagName: ""}}); err == nil {
		t.Error("expected an error when no release carries a tag")
	}
}
