package cliapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/selfupdate"
	"github.com/opensave/opensave/internal/version"
)

// The CLI updates itself. Someone running OpenSave headless — a server, a
// Deck in Game Mode — has no desktop app to click an update banner in, and
// telling them to re-run an install script to pick up a bug fix is a good way
// to have them not pick it up.

// updateRepo is the GitHub "owner/repo" releases are read from. Kept in step
// with the desktop app's constant of the same name.
const updateRepo = "Liquid-co/OpenSave"

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func cmdUpdate(args []string) int {
	asJSON, args := jsonFlag(args)
	checkOnly := false
	for _, a := range args {
		switch a {
		case "--check", "-n":
			checkOnly = true
		case "--help", "-h":
			fmt.Fprintln(os.Stderr, updateUsage)
			return 0
		}
	}

	rel, err := latestRelease()
	if err != nil {
		return fail(asJSON, fmt.Errorf("couldn't check for updates: %w", err))
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	newer := version.Compare(latest, version.Version) > 0

	if asJSON {
		return emitJSON(map[string]any{
			"current": version.Version, "latest": latest,
			"updateAvailable": newer, "url": rel.HTMLURL,
		})
	}

	if !newer {
		section("Update")
		field("installed", bold(version.Version))
		field("latest release", latest)
		fmt.Println()
		// Anyone testing a pre-release is ahead of the newest stable build.
		// Telling them they're "on the latest version" while showing a lower
		// number next to it reads like something is broken.
		if version.Compare(version.Version, latest) > 0 {
			success("You're ahead of the latest release — nothing to update to.")
			note("Pre-release builds are never offered here; grab them from the release page.")
		} else {
			success("You're on the latest version.")
		}
		fmt.Println()
		return 0
	}

	section("Update")
	field("installed", version.Version)
	field("available", bold(accent(latest)))
	fmt.Println()

	if checkOnly {
		hint("opensave update      install it")
		fmt.Println()
		return 0
	}

	assetURL, assetName := pickCLIAsset(rel)
	if assetURL == "" {
		warning("This release has no download for %s/%s.", runtime.GOOS, runtime.GOARCH)
		note("Release page: " + rel.HTMLURL)
		fmt.Println()
		return 1
	}

	exe, err := os.Executable()
	if err != nil {
		return fail(asJSON, err)
	}
	if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
		exe = resolved
	}
	// Refuse before downloading rather than after: a system-wide install is
	// the common case for the CLI, and finding out at the swap would mean a
	// pointless download and a scarier-looking failure.
	if !selfupdate.CanStageUpdate(exe) {
		// Names the binary, not just its folder: the folder can be writable
		// while this particular file is not, which is the case that made the
		// old check pass and the update fail anyway.
		warning("%s can't be replaced by this user.", exe)
		note("Re-run with sudo, or reinstall with the install script.")
		hint("curl -fsSL https://raw.githubusercontent.com/" + updateRepo + "/main/scripts/install.sh | sh")
		fmt.Println()
		return 1
	}

	staged := exe + ".new"
	defer os.Remove(staged)

	if err := downloadCLIAsset(assetURL, assetName, staged); err != nil {
		fmt.Println()
		return fail(asJSON, err)
	}

	_, old, err := selfupdate.Swap(staged)
	if err != nil {
		return fail(asJSON, err)
	}
	// The replaced binary is this running process on Windows, so it can't be
	// deleted yet. Nothing depends on it — the next invocation is the new
	// build — so a best-effort sweep is enough.
	go selfupdate.CleanupOld(old)

	success("Updated %s → %s", version.Version, bold(latest))
	note("Already-running daemons keep the old build until restarted.")
	hint("opensave daemon stop && opensave daemon start")
	fmt.Println()
	return 0
}

// downloadCLIAsset fetches the asset and leaves an executable at dest,
// unpacking it first when the platform ships the CLI inside a tarball.
func downloadCLIAsset(url, name, dest string) error {
	progress := progressBar()

	if strings.HasSuffix(strings.ToLower(name), ".tar.gz") || strings.HasSuffix(strings.ToLower(name), ".tgz") {
		archive, err := os.CreateTemp("", "opensave-update-*.tar.gz")
		if err != nil {
			return err
		}
		archivePath := archive.Name()
		archive.Close()
		defer os.Remove(archivePath)

		if err := selfupdate.Download(url, archivePath, progress); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Println()
		if err := selfupdate.ExtractFromTarGz(archivePath, "opensave-cli", dest); err != nil {
			return fmt.Errorf("unpack failed: %w", err)
		}
		return nil
	}

	if err := selfupdate.Download(url, dest, progress); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Println()
	return nil
}

// progressBar returns a callback that redraws a single line, so a slow
// download looks alive instead of looking hung. Falls back to nothing when
// output isn't a terminal — a redrawn line in a log file is noise.
func progressBar() func(done, total int64) {
	if !colorEnabled {
		return nil
	}
	return func(done, total int64) {
		if total <= 0 {
			fmt.Printf("\r  downloading %s", humanBytes(done))
			return
		}
		pct := int(done * 100 / total)
		const width = 24
		filled := pct * width / 100
		bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
		fmt.Printf("\r  %s %3d%%  %s / %s", accent(bar), pct, humanBytes(done), humanBytes(total))
	}
}

// pickCLIAsset selects the release asset carrying the CLI for this platform.
// Windows publishes opensave-cli.exe on its own; Linux ships it inside the
// per-architecture tarball alongside the app and relay.
func pickCLIAsset(rel *releaseInfo) (url, name string) {
	wantTarball := "opensave-linux-" + runtime.GOARCH + ".tar.gz"
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		switch runtime.GOOS {
		case "windows":
			if lower == "opensave-cli.exe" {
				return a.URL, a.Name
			}
		case "linux":
			if lower == wantTarball {
				return a.URL, a.Name
			}
		}
	}
	return "", ""
}

func latestRelease() (*releaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OpenSave/"+version.Version)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &rel, nil
}

const updateUsage = `usage: opensave update [--check]

  --check   Report whether a newer version exists, without installing it

Updates this CLI binary in place from the latest GitHub release. Pre-releases
are never offered; install those from the release page yourself.`
