// Package version is the single source of truth for the running build's
// identity: the semantic version plus an ldflags-injected build timestamp.
// The build time is what lets two devices on the SAME version (e.g. dev
// builds) still tell whose binary is newer for peer-to-peer updates.
package version

import (
	"strconv"
	"strings"
)

// Version is the app's semantic version. Overridable via
// -ldflags "-X github.com/opensave/opensave/internal/version.Version=…".
var Version = "2.2.1"

// BuildTime is the unix-seconds build timestamp, injected via
// -ldflags "-X github.com/opensave/opensave/internal/version.BuildTime=…".
// Empty for plain `go build`/`go test` binaries.
var BuildTime = ""

// BuildTimeMs returns the build timestamp in milliseconds, 0 when unknown.
func BuildTimeMs() int64 {
	if BuildTime == "" {
		return 0
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(BuildTime), 10, 64)
	if err != nil {
		return 0
	}
	return secs * 1000
}

// IsPreRelease reports whether a version string names a pre-release —
// anything carrying a semver suffix, like "2.2.1-beta.1". A leading "v" is
// accepted so a tag name can be passed straight in.
//
// This is what puts someone on the beta channel: running a pre-release means
// wanting the next one, because the alternative is being stranded on a build
// with no update path until the final release catches up.
func IsPreRelease(v string) bool {
	_, pre, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(v), "v"), "-")
	return pre != ""
}

// Compare returns -1, 0, or 1 comparing dotted numeric versions
// (e.g. "2.1.0" vs "2.0.3"). Non-numeric or missing parts count as 0.
func Compare(a, b string) int {
	// Split off any semver pre-release suffix ("2.1.0-beta.1"): the numeric
	// core compares first, and on an equal core a pre-release sorts BELOW
	// the final release (so beta users are offered the stable build).
	aCore, aPre, _ := strings.Cut(a, "-")
	bCore, bPre, _ := strings.Cut(b, "-")
	if c := compareCore(aCore, bCore); c != 0 {
		return c
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1 // release > pre-release
	case bPre == "":
		return -1
	default:
		return comparePreRelease(aPre, bPre)
	}
}

// comparePreRelease orders two semver pre-release suffixes ("beta.2" against
// "beta.10").
//
// Identifier by identifier, numerically when both sides are numeric. Plain
// string ordering is wrong the moment a series reaches double digits: "beta.10"
// sorts below "beta.9" because "1" precedes "9", so everyone on beta.9 would be
// told they were up to date forever. A suffix that is a prefix of the other
// sorts lower, so "beta" precedes "beta.1".
func comparePreRelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		switch {
		case i >= len(as):
			return -1 // a ran out first: fewer identifiers sorts lower
		case i >= len(bs):
			return 1
		}
		ai, aErr := strconv.Atoi(as[i])
		bi, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		case aErr == nil:
			return -1 // numeric identifiers sort below alphanumeric ones
		case bErr == nil:
			return 1
		case as[i] != bs[i]:
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func compareCore(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(as) {
			ai, _ = strconv.Atoi(strings.TrimSpace(as[i]))
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(strings.TrimSpace(bs[i]))
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}

// NewerThanLocal reports whether a peer's build (version + build time) is
// strictly newer than this one. Same-version builds compare by build time
// with a one-minute skew margin; unknown build times never count as newer.
func NewerThanLocal(peerVersion string, peerBuildMs int64) bool {
	switch Compare(peerVersion, Version) {
	case 1:
		return true
	case -1:
		return false
	}
	local := BuildTimeMs()
	if peerBuildMs == 0 || local == 0 {
		return false
	}
	return peerBuildMs > local+60_000
}
