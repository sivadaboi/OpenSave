package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

// InstallUpdateFromPeer downloads the newer build a paired peer is running
// and installs it. Returns immediately; progress and the final outcome
// arrive over the "app-update" WS broadcast (the app restarts on success).
func (a *App) InstallUpdateFromPeer(peerID string) string {
	if a.daemon == nil {
		return "app is not fully started"
	}
	go func() {
		exe, err := os.Executable()
		if err != nil {
			a.updateEvent("error", 0, err.Error())
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
func (a *App) InstallUpdateFromURL(url string) string {
	if !strings.HasPrefix(url, "https://") {
		return "update URL must be https"
	}
	go func() {
		exe, err := os.Executable()
		if err != nil {
			a.updateEvent("error", 0, err.Error())
			return
		}
		dest := exe + ".new"
		defer os.Remove(dest)

		a.updateEvent("downloading", 0, "")
		if err := downloadToFile(url, dest, func(done, total int64) {
			if total > 0 {
				a.updateEvent("downloading", int(done*100/total), "")
			}
		}); err != nil {
			a.updateEvent("error", 0, "download failed: "+err.Error())
			return
		}
		a.finishInstall(dest)
	}()
	return ""
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
	runtime.Quit(a.ctx)
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
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return err
	}
	if head[0] != 'M' || head[1] != 'Z' {
		return fmt.Errorf("downloaded file is not a Windows executable")
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
