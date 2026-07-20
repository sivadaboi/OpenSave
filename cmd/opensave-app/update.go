package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/version"
)

// updateRepo is the GitHub "owner/repo" whose releases are checked for a
// newer version. Change this one line if the project moves.
const updateRepo = "sivadaboi/OpenSave"

// CheckForUpdate best-effort asks GitHub for the latest published release
// and reports whether it is newer than the running build. Any failure
// (offline, rate-limited, no releases) resolves to "no update" so the UI
// never blocks or errors on it.
func (a *App) CheckForUpdate() map[string]any {
	none := map[string]any{"available": false, "current": AppVersion}

	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		return none
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OpenSave/"+AppVersion)

	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		return none
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return none
	}

	var rel struct {
		TagName    string         `json:"tag_name"`
		HTMLURL    string         `json:"html_url"`
		Prerelease bool           `json:"prerelease"`
		Body       string         `json:"body"`
		Assets     []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return none
	}
	if rel.Prerelease || rel.TagName == "" {
		return none
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	if compareVersions(latest, AppVersion) <= 0 {
		return none
	}
	url := rel.HTMLURL
	if url == "" {
		url = "https://github.com/" + updateRepo + "/releases/latest"
	}

	// A directly-downloadable asset enables one-click in-app install;
	// without one the UI falls back to opening the release page. The asset
	// is OS-specific: the portable app binary on Windows, the Linux tarball
	// on Linux. Never the CLI/relay/installer sub-artifacts.
	//
	// Flatpak installs can't self-swap (/app is read-only) — leave assetUrl
	// empty so the banner opens the release page, where the .flatpak lives.
	assetURL := ""
	if !runningInFlatpak() {
		assetURL = selectUpdateAsset(rel.Assets)
	}

	notes := rel.Body
	if len(notes) > 4000 {
		notes = notes[:4000] + "\n…"
	}
	return map[string]any{
		"available": true,
		"current":   AppVersion,
		"latest":    latest,
		"url":       url,
		"assetUrl":  assetURL,
		"notes":     notes,
		"flatpak":   runningInFlatpak(),
	}
}

// releaseAsset is the subset of a GitHub release asset the updater needs.
type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// selectUpdateAsset picks the OS-appropriate one-click update asset:
// OpenSave.exe on Windows, the linux tarball on Linux. Returns "" when no
// matching asset exists (the UI then opens the release page instead).
func selectUpdateAsset(assets []releaseAsset) string {
	return selectUpdateAssetFor(assets, runtime.GOOS)
}

func selectUpdateAssetFor(assets []releaseAsset, goos string) string {
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		switch goos {
		case "windows":
			// Portable app binary only — not the installer/cli/relay.
			if name == "opensave.exe" {
				return a.BrowserDownloadURL
			}
		case "linux":
			if strings.HasPrefix(name, "opensave-linux") &&
				(strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")) {
				return a.BrowserDownloadURL
			}
		}
	}
	return ""
}

// compareVersions is kept as a thin alias so existing tests keep working;
// the canonical implementation lives in internal/version.
func compareVersions(a, b string) int { return version.Compare(a, b) }
