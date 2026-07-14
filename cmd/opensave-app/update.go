package main

import (
	"encoding/json"
	"net/http"
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
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Body       string `json:"body"`
		Assets     []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
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

	// A directly-downloadable .exe asset enables one-click in-app install;
	// without one the UI falls back to opening the release page. Releases
	// carry several executables (NSIS installer, CLI, relay) — only the
	// portable app binary may ever be swapped in over the running app.
	assetURL := ""
	for _, asset := range rel.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, ".exe") {
			continue
		}
		if strings.Contains(name, "installer") || strings.Contains(name, "setup") ||
			strings.Contains(name, "cli") || strings.Contains(name, "relay") {
			continue
		}
		if name == "opensave.exe" {
			assetURL = asset.BrowserDownloadURL
			break
		}
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
	}
}

// compareVersions is kept as a thin alias so existing tests keep working;
// the canonical implementation lives in internal/version.
func compareVersions(a, b string) int { return version.Compare(a, b) }
