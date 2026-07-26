// Package selfupdate holds the mechanics of replacing a running OpenSave
// binary with a newer one: downloading it, checking it is actually an
// executable for this platform, and swapping it into place.
//
// Both the desktop app and the CLI update themselves, and the parts that can
// go badly wrong — writing over the binary the user launches, running
// whatever a download returned — are the parts worth having exactly one copy
// of.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// minPlausibleBinary is the floor below which a download cannot be a real
// build. Its job is to reject an error page or a truncated transfer before
// anything is executed, not to be an accurate size.
const minPlausibleBinary = 1 << 20

// Download streams url to path, calling progress at most a few times a second
// with bytes done and the total when the server declared one (-1 if not).
func Download(url, path string, progress func(done, total int64)) error {
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
	var lastReport time.Time
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

// ValidateExecutable checks that path is an executable of plausible size for
// the running platform, so a captive-portal login page, an HTML error, or a
// half-finished download can never be swapped in and launched.
//
// Matching the platform's format also rejects a wrong-OS build — a Windows PE
// will not run on Linux — which matters because these binaries can arrive
// from a paired peer as well as from a release.
func ValidateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < minPlausibleBinary {
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
	switch runtime.GOOS {
	case "windows":
		if head[0] != 'M' || head[1] != 'Z' { // PE/COFF
			return fmt.Errorf("downloaded file is not a Windows executable")
		}
	default:
		if head[0] != 0x7F || head[1] != 'E' || head[2] != 'L' || head[3] != 'F' {
			return fmt.Errorf("downloaded file is not a Linux executable")
		}
	}
	return nil
}

// ExtractFromTarGz writes the first regular file whose base name matches
// wantName out to dest, with the executable bit set. Linux releases ship the
// app, CLI and relay together in one tarball.
func ExtractFromTarGz(tarGzPath, wantName, dest string) error {
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
			return fmt.Errorf("%s not found in archive", wantName)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != wantName {
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

// Swap replaces the running executable with newExePath. It returns the path
// that was actually replaced and where the previous build was moved to, so
// the caller relaunches and rolls back against the same path this used —
// symlinks are resolved here, and a caller re-deriving the path from
// os.Executable would get the link instead, then roll back by renaming the
// old binary over it and destroying the link.
//
// Windows will not let a running executable be overwritten, but it will let
// one be renamed — so the current build moves aside to <exe>.old, the new one
// takes its place, and the leftover is deleted once this process has exited.
// Every failure rolls back, because a half-applied update leaves the user
// with no working binary at all.
func Swap(newExePath string) (exePath, oldPath string, err error) {
	if err := ValidateExecutable(newExePath); err != nil {
		return "", "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
		exe = resolved
	}

	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return "", "", fmt.Errorf("move the current build aside: %w", err)
	}
	if err := os.Rename(newExePath, exe); err != nil {
		_ = os.Rename(old, exe) // put it back; the user keeps a working binary
		return "", "", fmt.Errorf("put the new build in place: %w", err)
	}
	return exe, old, nil
}

// DirWritable reports whether this process can create files in dir — false
// for a Program Files or /usr/local install without the rights to change it,
// which is exactly when a swap must not be attempted.
func DirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".opensave-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

// CleanupOld removes a binary left behind by a previous swap, retrying while
// the old process finishes exiting and releases its lock on Windows.
func CleanupOld(path string) {
	for i := 0; i < 30; i++ { // ~15s
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
