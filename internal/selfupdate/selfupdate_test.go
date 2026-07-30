package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeBinary builds bytes that pass ValidateExecutable on this platform: the
// right magic header and past the minimum size.
func fakeBinary() []byte {
	var head []byte
	if runtime.GOOS == "windows" {
		head = []byte{'M', 'Z', 0x90, 0x00}
	} else {
		head = []byte{0x7F, 'E', 'L', 'F'}
	}
	return append(head, bytes.Repeat([]byte{0x42}, minPlausibleBinary)...)
}

// This is the guard between "a download finished" and "we ran it". Everything
// it rejects is something that has actually reached users of other updaters:
// a captive-portal login page, a truncated transfer, a wrong-OS build.
func TestValidateExecutableRejectsWhatIsNotABinary(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("accepts a plausible binary", func(t *testing.T) {
		if err := ValidateExecutable(write("good", fakeBinary())); err != nil {
			t.Errorf("valid binary rejected: %v", err)
		}
	})

	t.Run("rejects an HTML error page", func(t *testing.T) {
		page := []byte("<!DOCTYPE html><html><body>Sign in to the network</body></html>")
		page = append(page, bytes.Repeat([]byte(" "), minPlausibleBinary)...)
		err := ValidateExecutable(write("portal", page))
		if err == nil {
			t.Fatal("a captive-portal page was accepted as an executable")
		}
		if !strings.Contains(err.Error(), "executable") {
			t.Errorf("unhelpful error: %v", err)
		}
	})

	t.Run("rejects a truncated download", func(t *testing.T) {
		if err := ValidateExecutable(write("short", fakeBinary()[:1024])); err == nil {
			t.Error("a truncated file was accepted")
		}
	})

	t.Run("rejects a wrong-OS binary", func(t *testing.T) {
		var wrong []byte
		if runtime.GOOS == "windows" {
			wrong = append([]byte{0x7F, 'E', 'L', 'F'}, bytes.Repeat([]byte{0}, minPlausibleBinary)...)
		} else {
			wrong = append([]byte{'M', 'Z', 0x90, 0x00}, bytes.Repeat([]byte{0}, minPlausibleBinary)...)
		}
		if err := ValidateExecutable(write("wrongos", wrong)); err == nil {
			t.Error("a binary for another OS was accepted — it would be swapped in and fail to launch")
		}
	})

	t.Run("rejects a missing file", func(t *testing.T) {
		if err := ValidateExecutable(filepath.Join(dir, "nope")); err == nil {
			t.Error("a missing file was accepted")
		}
	})
}

func TestDownload(t *testing.T) {
	payload := fakeBinary()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()

	t.Run("writes the body and reports progress", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "out.bin")
		var lastDone int64
		if err := Download(srv.URL+"/ok", dest, func(done, total int64) { lastDone = done }); err != nil {
			t.Fatalf("Download: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("downloaded %d bytes, want %d", len(got), len(payload))
		}
		if lastDone != int64(len(payload)) {
			t.Errorf("final progress reported %d, want %d", lastDone, len(payload))
		}
	})

	t.Run("surfaces a non-200 instead of writing the error body", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "out.bin")
		err := Download(srv.URL+"/missing", dest, nil)
		if err == nil {
			t.Fatal("a 404 was treated as a successful download")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("error should name the status, got %v", err)
		}
	})
}

func TestExtractFromTarGz(t *testing.T) {
	// Linux releases ship the app, CLI and relay together, so picking the
	// right member out of the archive is the whole job.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"opensave-linux/opensave", "APP"},
		{"opensave-linux/opensave-cli", "CLI"},
		{"opensave-linux/opensave-relay", "RELAY"},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	archive := filepath.Join(dir, "rel.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}

	for _, want := range []struct{ member, body string }{
		{"opensave", "APP"},
		{"opensave-cli", "CLI"},
		{"opensave-relay", "RELAY"},
	} {
		dest := filepath.Join(dir, want.member+".out")
		if err := ExtractFromTarGz(archive, want.member, dest); err != nil {
			t.Fatalf("extract %s: %v", want.member, err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want.body {
			t.Errorf("%s = %q, want %q — the wrong archive member was extracted", want.member, got, want.body)
		}
	}

	if err := ExtractFromTarGz(archive, "not-in-there", filepath.Join(dir, "x")); err == nil {
		t.Error("extracting a missing member should fail rather than leave an empty file")
	}
}

// Swap must never leave the user without a working binary. A failure at any
// point has to put the original back.
func TestSwapRejectsBadCandidateAndLeavesOriginalAlone(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.bin")
	if err := os.WriteFile(bad, []byte("not an executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The running test binary is what os.Executable() reports; validation
	// must fail before Swap touches it.
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot determine test executable")
	}
	before, err := os.Stat(exe)
	if err != nil {
		t.Skip("cannot stat test executable")
	}

	if _, _, err := Swap(bad); err == nil {
		t.Fatal("Swap accepted a file that is not an executable")
	}
	after, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("the running binary disappeared after a rejected swap: %v", err)
	}
	if before.Size() != after.Size() {
		t.Error("the running binary was modified by a swap that should have been refused")
	}
	if _, err := os.Stat(exe + ".old"); err == nil {
		os.Remove(exe + ".old")
		t.Error("a rejected swap left a .old file behind")
	}
}

// CanStageUpdate decides whether the in-place swap is attempted or the
// elevating installer runs instead. Getting it wrong in the optimistic
// direction is what shipped: the old check created a randomly named temp file
// in the same folder, that succeeded, and writing <exe>.new then failed with
// "Access is denied" — leaving the user on the old version with a filesystem
// error they could do nothing about.
func TestCanStageUpdate(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "opensave.exe")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !CanStageUpdate(exe) {
		t.Error("a writable location reported as unstageable — the app would send the user to the installer for no reason")
	}

	// It must probe, not litter: a staging file left behind would be picked
	// up as a half-finished update.
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Errorf("the probe left %s behind", exe+".new")
	}

	// A binary whose directory does not exist cannot be staged.
	missing := filepath.Join(dir, "no-such-dir", "opensave.exe")
	if CanStageUpdate(missing) {
		t.Error("a missing directory reported as stageable — the swap would fail later instead of refusing up front")
	}
}

// A leftover staging file from an interrupted update must not make the probe
// report failure: that would send every subsequent update to the installer
// until someone deleted a file they cannot see.
func TestCanStageUpdateOverwritesLeftoverStagingFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "opensave.exe")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".new", []byte("interrupted download"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !CanStageUpdate(exe) {
		t.Error("a leftover .new file made the probe report the location unwritable")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error("the leftover staging file survived the probe")
	}
	// The real binary must be untouched by any of this.
	if raw, err := os.ReadFile(exe); err != nil || string(raw) != "binary" {
		t.Errorf("the probe modified the binary itself: %q, %v", raw, err)
	}
}

func TestCleanupOldIsQuietWhenNothingToRemove(t *testing.T) {
	// Must not block for its full retry budget when the file was never there.
	done := make(chan struct{})
	go func() {
		CleanupOld(filepath.Join(t.TempDir(), "absent"))
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutAfterSeconds(5):
		t.Fatal("CleanupOld spun on a file that does not exist")
	}
}

func timeoutAfterSeconds(n int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(n) * time.Second)
		close(ch)
	}()
	return ch
}

// The distinction that matters, made concrete: a directory can be perfectly
// writable while the one path the update needs is not usable. Here <exe>.new
// already exists as a directory, so creating it as a file cannot succeed —
// yet creating a differently-named file alongside it succeeds happily, which
// is precisely what the old probe did and why it passed while the real write
// failed.
func TestCanStageUpdateCatchesWhatAProbeFileWouldMiss(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "opensave.exe")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(exe+".new", 0o755); err != nil {
		t.Fatal(err)
	}

	// The old approach: any temp file in the same directory. Still fine.
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		t.Fatalf("the directory is not writable, so this test proves nothing: %v", err)
	}
	probe.Close()
	_ = os.Remove(probe.Name())

	// The real question, asked about the real path.
	if CanStageUpdate(exe) {
		t.Error("reported stageable when <exe>.new cannot be created as a file — " +
			"this is the shape of the 2.1.1 update failure: writable folder, unusable target")
	}
}
