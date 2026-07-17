package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/version"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Self-update: replace the running executable with a newer build and
// restart. Windows can't overwrite a running exe, but it CAN be renamed —
// so the dance is: rename running exe aside (.old), move the downloaded
// build into its place, spawn it, quit. The new process deletes the .old
// leftover once this one has exited (see cleanupReplacedBinary).

// updateEvent broadcasts install progress to the dashboard so the UI can
// show a live banner ("downloading 42%…") instead of appearing to hang.
func (a *App) updateEvent(state string, pct int, errMsg string) {
	if a.server == nil {
		return
	}
	a.server.Hub.Broadcast("app-update", map[string]any{
		"state": state, "percentage": pct, "error": errMsg,
	})
}

// runningInFlatpak reports whether the app runs inside a Flatpak sandbox,
// where /app is read-only and the rename-swap self-update cannot work —
// updates arrive through flatpak itself (or a newer bundle from the
// release page).
func runningInFlatpak() bool {
	if os.Getenv("FLATPAK_ID") != "" {
		return true
	}
	_, err := os.Stat("/.flatpak-info")
	return err == nil
}

const flatpakUpdateMsg = "OpenSave is installed as a Flatpak, which updates through Flatpak itself — " +
	"run \"flatpak update\", or install the newer OpenSave.flatpak from the GitHub release page."

// dirWritable reports whether the current process can create files in dir.
// False for Program Files installs running without elevation — the case
// where the rename-swap self-update cannot work.
func dirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".opensave-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

// InstallUpdateFromPeer downloads the newer build a paired peer is running
// and installs it. Returns immediately; progress and the final outcome
// arrive over the "app-update" WS broadcast (the app restarts on success).
func (a *App) InstallUpdateFromPeer(peerID string) string {
	if a.daemon == nil {
		return "app is not fully started"
	}
	if runningInFlatpak() {
		return flatpakUpdateMsg
	}
	go func() {
		exe, err := os.Executable()
		if err != nil {
			a.updateEvent("error", 0, err.Error())
			return
		}
		if !dirWritable(filepath.Dir(exe)) {
			a.updateEvent("error", 0,
				"OpenSave is installed in a protected folder (like Program Files), which peer updates can't replace. "+
					"Use the update banner to install from GitHub instead — that path runs the installer with the proper permissions.")
			return
		}
		dest := exe + ".new"
		defer os.Remove(dest)

		a.updateEvent("downloading", 0, "")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		build, err := a.daemon.P2P.DownloadPeerBinary(ctx, peerID, dest, func(done, total int64) {
			if total > 0 {
				a.updateEvent("downloading", int(done*100/total), "")
			}
		})
		if err != nil {
			a.updateEvent("error", 0, "download from peer failed: "+err.Error())
			return
		}
		a.daemon.Log.Log("info", fmt.Sprintf("downloaded OpenSave %s (build %d) from peer; installing", build.AppVersion, build.BuildTimeMs))
		a.finishInstall(dest)
	}()
	return ""
}

// InstallUpdateFromURL downloads a release asset (the .exe from a GitHub
// release) and installs it. Same async contract as InstallUpdateFromPeer.
//
// Installs living in a protected folder (Program Files) can't be swapped
// by an unelevated rename — for those, the NSIS installer is downloaded
// and launched instead; it requests UAC elevation itself.
func (a *App) InstallUpdateFromURL(url string) string {
	if !strings.HasPrefix(url, "https://") {
		return "update URL must be https"
	}
	if runningInFlatpak() {
		return flatpakUpdateMsg
	}
	go func() {
		exe, err := os.Executable()
		if err != nil {
			a.updateEvent("error", 0, err.Error())
			return
		}
		// Windows Program Files installs can't be swapped unelevated — run
		// the NSIS installer (with its UAC prompt) instead.
		if runtime.GOOS == "windows" && !dirWritable(filepath.Dir(exe)) {
			a.installViaInstaller()
			return
		}

		a.updateEvent("downloading", 0, "")
		progress := func(done, total int64) {
			if total > 0 {
				a.updateEvent("downloading", int(done*100/total), "")
			}
		}

		newBinary := exe + ".new"
		defer os.Remove(newBinary)

		if strings.HasSuffix(strings.ToLower(url), ".tar.gz") || strings.HasSuffix(strings.ToLower(url), ".tgz") {
			// Linux ships a tarball: download it, extract the app binary.
			archive, err := os.CreateTemp("", "opensave-update-*.tar.gz")
			if err != nil {
				a.updateEvent("error", 0, err.Error())
				return
			}
			archivePath := archive.Name()
			archive.Close()
			defer os.Remove(archivePath)

			if err := downloadToFile(url, archivePath, progress); err != nil {
				a.updateEvent("error", 0, "download failed: "+err.Error())
				return
			}
			if err := extractAppBinary(archivePath, newBinary); err != nil {
				a.updateEvent("error", 0, "unpack failed: "+err.Error())
				return
			}
		} else {
			// Windows portable exe: download straight to the swap file.
			if err := downloadToFile(url, newBinary, progress); err != nil {
				a.updateEvent("error", 0, "download failed: "+err.Error())
				return
			}
		}
		a.finishInstall(newBinary)
	}()
	return ""
}

// extractAppBinary pulls the OpenSave app binary out of a release tarball
// (opensave-linux/opensave) and writes it to dest, executable.
func extractAppBinary(tarGzPath, dest string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("app binary not found in archive")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The app binary is named "opensave" (not the cli/relay).
		base := filepath.Base(hdr.Name)
		if base != "opensave" {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// installViaInstaller fetches the latest release's NSIS installer into the
// temp dir and launches it, then quits so the installer can replace the
// app's files. Launching goes through ShellExecute (Start-Process) so the
// installer's elevation request produces a UAC prompt instead of failing.
func (a *App) installViaInstaller() {
	a.updateEvent("downloading", 0, "")

	instURL, err := fetchInstallerURL()
	if err != nil {
		a.updateEvent("error", 0, "couldn't locate the installer in the latest release: "+err.Error())
		return
	}

	tmp, err := os.CreateTemp("", "OpenSave.Setup-*.exe")
	if err != nil {
		a.updateEvent("error", 0, err.Error())
		return
	}
	dest := tmp.Name()
	tmp.Close()

	if err := downloadToFile(instURL, dest, func(done, total int64) {
		if total > 0 {
			a.updateEvent("downloading", int(done*100/total), "")
		}
	}); err != nil {
		os.Remove(dest)
		a.updateEvent("error", 0, "download failed: "+err.Error())
		return
	}
	if err := validateExecutable(dest); err != nil {
		os.Remove(dest)
		a.updateEvent("error", 0, err.Error())
		return
	}

	a.updateEvent("installing", 100, "")
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Start-Process -FilePath '"+dest+"'")
	if err := cmd.Start(); err != nil {
		os.Remove(dest)
		a.updateEvent("error", 0, "launch installer: "+err.Error())
		return
	}

	if a.daemon != nil {
		a.daemon.Log.Log("info", "update installer launched; quitting so it can replace the app")
	}
	a.updateEvent("restarting", 100, "")
	a.reallyQuit = true
	wailsruntime.Quit(a.ctx)
}

// fetchInstallerURL returns the download URL of the NSIS installer asset
// on the latest GitHub release.
func fetchInstallerURL() (string, error) {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OpenSave/"+AppVersion)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	for _, asset := range rel.Assets {
		name := strings.ToLower(asset.Name)
		if strings.HasSuffix(name, ".exe") &&
			(strings.Contains(name, "setup") || strings.Contains(name, "installer")) {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no installer asset on the latest release")
}

// finishInstall validates and applies a downloaded build, then restarts.
func (a *App) finishInstall(newExePath string) {
	a.updateEvent("installing", 100, "")
	if err := a.applyUpdate(newExePath); err != nil {
		a.updateEvent("error", 0, "install failed: "+err.Error())
		return
	}
	// applyUpdate quits the app on success; this line is never reached.
}

// applyUpdate swaps the running executable for newExePath and restarts.
func (a *App) applyUpdate(newExePath string) error {
	if err := validateExecutable(newExePath); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)

	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("move current build aside: %w", err)
	}
	if err := os.Rename(newExePath, exe); err != nil {
		// Roll back so the app stays launchable.
		_ = os.Rename(old, exe)
		return fmt.Errorf("place new build: %w", err)
	}

	cmd := exec.Command(exe)
	// The child waits for this .old file to become deletable — i.e. for
	// this process to fully exit — before claiming the single-instance
	// lock. Without the wait, the new instance would see us still running,
	// signal us to show our window, and exit.
	cmd.Env = append(os.Environ(), "OPENSAVE_CLEANUP_OLD="+old)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(exe)
		_ = os.Rename(old, exe)
		return fmt.Errorf("launch new build: %w", err)
	}

	if a.daemon != nil {
		a.daemon.Log.Log("info", "update installed; restarting")
	}
	a.updateEvent("restarting", 100, "")
	a.reallyQuit = true
	wailsruntime.Quit(a.ctx)
	return nil
}

// validateExecutable sanity-checks that the downloaded file is a Windows
// PE binary of plausible size, so a captive-portal HTML page or truncated
// download can never replace the app.
func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < 1<<20 {
		return fmt.Errorf("downloaded file is too small (%d bytes) to be OpenSave", info.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return err
	}
	// Match the running platform's executable format. This also rejects a
	// wrong-OS binary from a peer (a Windows PE can't run on Linux, and
	// vice versa), so peer updates only apply a compatible build.
	switch runtime.GOOS {
	case "windows":
		if head[0] != 'M' || head[1] != 'Z' { // PE/COFF
			return fmt.Errorf("downloaded file is not a Windows executable")
		}
	default:
		if head[0] != 0x7F || head[1] != 'E' || head[2] != 'L' || head[3] != 'F' { // ELF
			return fmt.Errorf("downloaded file is not a Linux executable")
		}
	}
	return nil
}

// downloadToFile streams url to path with progress callbacks.
func downloadToFile(url, path string, progress func(done, total int64)) error {
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256<<10)
	lastReport := time.Time{}
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if progress != nil && time.Since(lastReport) > 300*time.Millisecond {
				lastReport = time.Now()
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if progress != nil {
		progress(done, total)
	}
	return nil
}

// stampVersionFile records the running build's identity in the data dir
// and returns the previously recorded version when it differs — i.e. this
// is the first run after an update. Returns "" on the very first run or
// when the build hasn't changed.
func stampVersionFile(homeDir string) (updatedFrom string) {
	path := filepath.Join(homeDir, "last-version")
	stamp := version.Version + "|" + strconv.FormatInt(version.BuildTimeMs(), 10)

	prev := ""
	if raw, err := os.ReadFile(path); err == nil {
		prev = strings.TrimSpace(string(raw))
	}
	if prev == stamp {
		return ""
	}
	_ = os.WriteFile(path, []byte(stamp), 0o666)
	if prev == "" {
		return "" // first run ever — nothing to announce
	}
	if i := strings.IndexByte(prev, '|'); i >= 0 {
		prev = prev[:i]
	}
	return prev
}

// cleanupReplacedBinary runs at startup. After an update, the previous
// build lives at <exe>.old and may still be exiting; deleting it doubles
// as the "old process has fully quit" gate before Wails takes the
// single-instance lock.
func cleanupReplacedBinary() {
	if old := os.Getenv("OPENSAVE_CLEANUP_OLD"); old != "" {
		for i := 0; i < 30; i++ { // up to ~15s for the old process to exit
			err := os.Remove(old)
			if err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		return
	}
	// Normal start: sweep any leftover from a previous update.
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}
