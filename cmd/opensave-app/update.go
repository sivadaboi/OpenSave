package main

import (
	"runtime"
	"strings"

	"github.com/opensave/opensave/internal/selfupdate"
	"github.com/opensave/opensave/internal/version"
)

// wantsPreReleases reports whether this device should be offered betas: the
// user asked for the channel, or is already running one.
func (a *App) wantsPreReleases() bool {
	channel := ""
	if a.daemon != nil {
		if s, err := a.daemon.Store.GetSettings(); err == nil {
			channel = s.UpdateChannel
		}
	}
	return selfupdate.WantsPreReleases(channel, AppVersion)
}

// updateRepo is the GitHub "owner/repo" whose releases are checked for a
// newer version. Change this one line if the project moves.
const updateRepo = "Liquid-co/OpenSave"

// CheckForUpdate best-effort asks GitHub for the latest published release
// and reports whether it is newer than the running build. Any failure
// (offline, rate-limited, no releases) resolves to "no update" so the UI
// never blocks or errors on it.
func (a *App) CheckForUpdate() map[string]any {
	none := map[string]any{"available": false, "current": AppVersion}

	rel, err := selfupdate.LatestRelease(updateRepo, "OpenSave/"+AppVersion, a.wantsPreReleases())
	if err != nil || rel.TagName == "" {
		return none
	}

	latest := rel.Version()
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
		"available":  true,
		"current":    AppVersion,
		"latest":     latest,
		"url":        url,
		"assetUrl":   assetURL,
		"notes":      notes,
		"flatpak":    runningInFlatpak(),
		"prerelease": rel.Prerelease,
	}
}

// releaseAsset is the subset of a GitHub release asset the updater needs.
type releaseAsset = selfupdate.Asset

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
