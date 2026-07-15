package version

import "testing"

func TestCompare_Prerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.0-beta.1", "2.0.1", 1},  // beta of a newer core > older release
		{"2.1.0-beta.1", "2.1.0", -1}, // pre-release < its final release
		{"2.1.0", "2.1.0-beta.1", 1},
		{"2.1.0-beta.1", "2.1.0-beta.2", -1},
		{"2.1.0-beta.1", "2.1.0-beta.1", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
