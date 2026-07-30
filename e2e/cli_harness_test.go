package e2e

// End-to-end coverage for the CLI binary itself.
//
// The rest of this suite drives the HTTP API directly, which leaves the CLI's
// own request and response layer untested — internal/cliapp sits around 9% of
// statements. That is not an academic gap. Two shipped bugs lived in it, and
// both were invisible to anything that mocked either side:
//
//   - the snapshot listing read "sizeBytes" where the API sends "size", so
//     every file printed as 0 B — a wrong field name unmarshals to the zero
//     value with no error at all;
//   - restore-file was sent "path" where the endpoint requires "relPath", so
//     single-file restore failed every time and restored nothing.
//
// Both sides were internally consistent and only disagreed with each other,
// so catching them needs the real binary talking to a real daemon over a real
// port. That is what this harness provides.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// cliBin is the binary under test, built once for the whole package.
var cliBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "opensave-cli-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli e2e: temp dir: %v\n", err)
		os.Exit(1)
	}
	cliBin = filepath.Join(dir, "opensave")
	if runtime.GOOS == "windows" {
		cliBin += ".exe"
	}

	build := exec.Command("go", "build", "-o", cliBin, "./cmd/opensave-cli")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cli e2e: building the CLI failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// repoRoot walks up from the test's working directory (e2e/) to the module
// root, so `go build ./cmd/...` resolves wherever the tests are invoked from.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return "."
}

// cli is one isolated CLI environment: its own home directory, database and
// daemon, so tests cannot see each other's games or settings — and cannot
// touch the real OpenSave install on the machine running them.
type cli struct {
	t    *testing.T
	home string
	port int
	// daemon is nil until startDaemon; commands that read the database
	// directly work without one.
	daemon *exec.Cmd
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	// Not t.TempDir(): the daemon holds the SQLite file open, and on Windows
	// t.TempDir's cleanup fails the test when a handle is still live. This
	// cleans up best-effort after the daemon has been asked to stop.
	home, err := os.MkdirTemp("", "opensave-home-*")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	c := &cli{t: t, home: home, port: freePort(t)}
	t.Cleanup(func() {
		c.stopDaemon()
		_ = os.RemoveAll(home)
	})
	return c
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// env isolates the child process's idea of home. os.UserHomeDir reads
// USERPROFILE on Windows and HOME elsewhere; both are set so the same harness
// works on either.
func (c *cli) env() []string {
	return append(os.Environ(),
		"USERPROFILE="+c.home,
		"HOME="+c.home,
		// Styling is suppressed when stdout is not a terminal, but be explicit:
		// escape codes in the captured output would break every assertion.
		"NO_COLOR=1",
	)
}

// run executes the CLI and returns its combined output and exit code.
func (c *cli) run(args ...string) (string, int) {
	c.t.Helper()
	cmd := exec.Command(cliBin, args...)
	cmd.Env = c.env()
	cmd.Dir = c.home
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		c.t.Fatalf("running %v: %v", args, err)
	}
	return buf.String(), code
}

// mustRun fails the test if the command does not succeed, reporting what the
// command actually printed — a bare "exit 1" is not diagnosable.
func (c *cli) mustRun(args ...string) string {
	c.t.Helper()
	out, code := c.run(args...)
	if code != 0 {
		c.t.Fatalf("`opensave %s` exited %d:\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// mustFail asserts a command rejects its input. Scripts depend on this: a
// failure that exits 0 makes a typo look like success.
func (c *cli) mustFail(args ...string) string {
	c.t.Helper()
	out, code := c.run(args...)
	if code == 0 {
		c.t.Errorf("`opensave %s` exited 0, expected a non-zero status:\n%s",
			strings.Join(args, " "), out)
	}
	return out
}

// startDaemon launches the daemon and waits for it to publish its address,
// which is how the CLI finds it.
func (c *cli) startDaemon() {
	c.t.Helper()
	cmd := exec.Command(cliBin, "daemon", "start", "--port", fmt.Sprint(c.port))
	cmd.Env = c.env()
	cmd.Dir = c.home
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		c.t.Fatalf("starting the daemon: %v", err)
	}
	c.daemon = cmd

	addrFile := filepath.Join(c.home, ".opensave", "daemon.addr")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(addrFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			// Published, but confirm it actually answers before returning.
			if _, code := c.run("daemon", "status"); code == 0 {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	c.t.Fatalf("daemon never became ready on port %d:\n%s", c.port, log.String())
}

func (c *cli) stopDaemon() {
	if c.daemon == nil {
		return
	}
	// Ask nicely first so the database is closed cleanly, then make sure.
	_, _ = c.run("daemon", "stop")
	done := make(chan struct{})
	go func() { _, _ = c.daemon.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = c.daemon.Process.Kill()
	}
	c.daemon = nil
}

// saveDir creates a save folder with the given files, relative paths allowed.
func (c *cli) saveDir(name string, files map[string]string) string {
	c.t.Helper()
	dir := filepath.Join(c.home, "saves", name)
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			c.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.t.Fatal(err)
	}
	return dir
}

func (c *cli) readSave(dir, rel string) string {
	c.t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(raw)
}

// snapshotIDs pulls snapshot ids out of `opensave snapshots` output. They are
// the only snap_-prefixed tokens printed, so this stays readable rather than
// threading --json through every history assertion.
func (c *cli) snapshotIDs(gameID string) []string {
	c.t.Helper()
	out := c.mustRun("snapshots", gameID)
	var ids []string
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "snap_") {
			ids = append(ids, f)
		}
	}
	return ids
}

// removeFile deletes a file inside a save dir, for tests that verify a
// snapshot can bring a deleted file back.
func removeFile(dir, rel string) error {
	return os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
}

// mustJSON runs a command expected to emit JSON and decodes it into out.
// Decoding rather than substring-matching the output: a value that merely
// appears somewhere in a table is not the same as the field being populated,
// and that difference has hidden real bugs.
func (c *cli) mustJSON(out any, args ...string) {
	c.t.Helper()
	raw := c.mustRun(args...)
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), out); err != nil {
		c.t.Fatalf("`opensave %s` did not emit decodable JSON: %v\n%s",
			strings.Join(args, " "), err, raw)
	}
}
