package selfupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/version"
)

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Release is a published GitHub release.
type Release struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Prerelease bool    `json:"prerelease"`
	Body       string  `json:"body"`
	Assets     []Asset `json:"assets"`
}

// Version is the release's version with any leading "v" removed.
func (r Release) Version() string { return strings.TrimPrefix(r.TagName, "v") }

// LatestRelease returns the newest release on a channel.
//
// includePreReleases is the beta channel. It matters because GitHub's
// "latest release" endpoint deliberately skips pre-releases, which is right
// for most people and leaves anyone running one with no update path at all:
// their build is newer than the newest stable, so they are told they are up to
// date until the final release overtakes them. On the beta channel the full
// list is fetched and the highest version wins — including a stable release,
// so a beta is never a one-way door.
func LatestRelease(repo, userAgent string, includePreReleases bool) (Release, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	if includePreReleases {
		// Enough to cover any run of betas between two stable releases
		// without paging; the newest are returned first.
		url = "https://api.github.com/repos/" + repo + "/releases?per_page=30"
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub returned %s", resp.Status)
	}

	if !includePreReleases {
		var rel Release
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return Release{}, err
		}
		if rel.TagName == "" {
			return Release{}, fmt.Errorf("no published release found")
		}
		return rel, nil
	}

	var all []Release
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return Release{}, err
	}
	return newestOf(all)
}

// newestOf picks the highest-versioned published release. Drafts are skipped
// by the API for unauthenticated callers; anything without a tag is ignored
// rather than trusted to sort sensibly.
func newestOf(all []Release) (Release, error) {
	var best Release
	found := false
	for _, r := range all {
		if r.TagName == "" {
			continue
		}
		if !found || version.Compare(r.Version(), best.Version()) > 0 {
			best, found = r, true
		}
	}
	if !found {
		return Release{}, fmt.Errorf("no published release found")
	}
	return best, nil
}

// WantsPreReleases reports whether this build should be offered pre-releases.
//
// Either the user asked for the beta channel, or they are already running a
// pre-release — the second case on its own, because someone who installed a
// beta has no way forward otherwise and would sit on it silently.
func WantsPreReleases(channel, currentVersion string) bool {
	return strings.EqualFold(strings.TrimSpace(channel), "beta") ||
		version.IsPreRelease(currentVersion)
}
