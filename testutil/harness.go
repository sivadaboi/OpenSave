// Package testutil provides the E2E harness: full in-process daemons with
// isolated data dirs and real HTTP servers on loopback, so tests exercise
// the same wire protocol two real devices would.
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/api"
	"github.com/opensave/opensave/internal/daemon"
)

// TestDaemon is one running daemon + API server.
type TestDaemon struct {
	T       *testing.T
	Daemon  *daemon.Daemon
	Server  *api.Server
	Addr    string // host:port
	Port    int
	SaveDir string
	// lastErrMu guards lastError. Tests drive one daemon from several
	// goroutines at once — firing concurrent syncs at the same game is a case
	// worth covering — and every request wrote this field, so the race
	// detector failed the whole package. Reporting a request's error through
	// a field shared by all of them was also wrong on its own terms: the
	// message a failing call printed could belong to a different call.
	lastErrMu sync.Mutex
	lastError string
}

// mustTempRoot makes a temp directory for one test daemon and schedules a
// retrying, best-effort removal.
//
// Registered before the caller's daemon-stop cleanup so it runs after it —
// t.Cleanup is last-in-first-out — because nothing can be deleted while the
// daemon still holds the database and the watched save tree open.
func mustTempRoot(t *testing.T, pattern string) string {
	t.Helper()
	root, err := os.MkdirTemp("", pattern)
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	t.Cleanup(func() {
		// A handle released a moment after the daemon stops is normal on
		// Windows; give it a few tries, then leave it to the OS rather than
		// failing a test that has already passed.
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(root); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return root
}

// TempDir is t.TempDir with a retrying, best-effort removal. Use it for any
// directory a daemon writes into — a cloud folder, a watched save tree — for
// the reason described on mustTempRoot: the standard one fails an otherwise
// passing test when a handle is still open, which on Windows is routine.
func TempDir(t *testing.T) string { return mustTempRoot(t, "opensave-test-*") }

// NewTestDaemon boots a daemon with an isolated home dir and API server on
// an OS-assigned port. Discovery is disabled; tests pair explicitly.
func NewTestDaemon(t *testing.T, name string) *TestDaemon {
	t.Helper()

	// Not t.TempDir(): its cleanup deletes the tree and FAILS THE TEST if
	// anything is still holding a handle. On Windows that is routine — an
	// antivirus scanner keeps a file open for a moment after it is written,
	// and these tests write constantly — so a run that passed every assertion
	// was reported as a failure for a directory that could not be removed on
	// the first attempt. It is why CI failed here on Windows while the same
	// suite passed on Linux, and the CLI harness already avoids t.TempDir for
	// this reason.
	//
	// Removal is retried and, in the end, best-effort: the operating system
	// reclaims a test's temp directory regardless, and a stubborn handle says
	// nothing about whether the code under test is correct.
	root := mustTempRoot(t, "opensave-e2e-*")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o777); err != nil {
		t.Fatal(err)
	}
	d, err := daemon.New(daemon.Options{HomeOverride: home, DisableDiscovery: true})
	if err != nil {
		t.Fatalf("daemon.New(%s): %v", name, err)
	}
	// Hermetic: no background Ludusavi manifest download from test daemons
	// (races t.TempDir cleanup on Windows, wastes 17 MB per daemon).
	d.Scanner.ManifestURL = ""

	// Set the device name for readable pairing flows.
	settings, err := d.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.DeviceName = name
	if err := d.Store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start(%s): %v", name, err)
	}

	srv := api.New(d)
	addr, err := srv.Start(0)
	if err != nil {
		t.Fatalf("api.Start(%s): %v", name, err)
	}

	// Persist the real bound port: handshakes tell the counterpart to call
	// back on settings.Port.
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	settings, _ = d.Store.GetSettings()
	settings.Port = port
	if err := d.Store.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}

	// Under the same root, for the same reason: the watcher holds handles on
	// this tree for as long as the daemon runs.
	saveDir := filepath.Join(root, "saves")
	if err := os.MkdirAll(saveDir, 0o777); err != nil {
		t.Fatal(err)
	}

	td := &TestDaemon{T: t, Daemon: d, Server: srv, Addr: addr, Port: port, SaveDir: saveDir}
	t.Cleanup(func() {
		srv.Stop()
		d.Stop()
	})
	return td
}

// API performs a JSON request against this daemon's API and decodes the
// response into out (out may be nil). Non-2xx responses fail the test
// unless allowError is set.
func (td *TestDaemon) API(method, path string, body any, out any) {
	td.T.Helper()
	status, errBody := td.apiRaw(method, path, body, out)
	if status >= 400 {
		td.T.Fatalf("%s %s -> %d: %s", method, path, status, errBody)
	}
}

// LastError returns the response body of the most recent 4xx/5xx seen by any
// request on this daemon. Prefer the value API reports in its own failure
// message: with concurrent callers this is whichever request finished last.
func (td *TestDaemon) LastError() string {
	td.lastErrMu.Lock()
	defer td.lastErrMu.Unlock()
	return td.lastError
}

// APIStatus performs a request and returns the HTTP status without failing
// the test — for calls that may legitimately return a 4xx.
func (td *TestDaemon) APIStatus(method, path string, body any, out any) int {
	td.T.Helper()
	status, _ := td.apiRaw(method, path, body, out)
	return status
}

// apiRaw returns the status and, for a 4xx/5xx, the response body — returned
// rather than only stashed, so a caller reports its own error and concurrent
// callers cannot overwrite each other's.
func (td *TestDaemon) apiRaw(method, path string, body any, out any) (int, string) {
	td.T.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			td.T.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, "http://"+td.Addr+path, reader)
	if err != nil {
		td.T.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		td.T.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	// Read the body first so a failure can be reported with the reason in it.
	// Decoding straight into out discarded the error payload on a 4xx/5xx,
	// which left "POST /api/... -> 500" as the entire diagnosis — enough to
	// know something broke and nothing else.
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		td.T.Fatalf("%s %s: reading response: %v", method, path, readErr)
	}
	errBody := ""
	if resp.StatusCode >= 400 {
		errBody = strings.TrimSpace(string(raw))
	}
	td.lastErrMu.Lock()
	td.lastError = errBody
	td.lastErrMu.Unlock()

	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, errBody
}

// NodeID returns this daemon's peer identity.
func (td *TestDaemon) NodeID() string {
	settings, err := td.Daemon.Store.GetSettings()
	if err != nil {
		td.T.Fatal(err)
	}
	return settings.NodeID
}

// PairWith performs the full handshake dance: td initiates, other
// approves, and both sides end up with a persisted online peer.
func (td *TestDaemon) PairWith(other *TestDaemon) {
	td.T.Helper()

	td.API(http.MethodPost, "/api/peers/pair", map[string]any{
		"address": "127.0.0.1", "port": other.Port,
	}, nil)

	// The handshake lands as a pending request on the other side.
	if !WaitFor(10*time.Second, func() bool {
		return len(other.Daemon.P2P.Pairing.PendingRequests()) > 0
	}) {
		td.T.Fatal("handshake never arrived at the counterpart")
	}

	other.API(http.MethodPost, "/api/peers/approve", map[string]any{"peerId": td.NodeID()}, nil)

	// Both sides must now know each other, AND consider each other online.
	//
	// Knowing is not enough to sync: a sync only attempts peers whose status
	// is "online", and that is set by a periodic ping rather than by the
	// handshake. Returning as soon as the peer row existed meant a sync fired
	// immediately afterwards found no peers, did nothing, and reported no
	// error — so tests that fire one sync and then wait for the transfer sat
	// out their whole timeout waiting for something that was never started.
	// Rare at full speed and common under the race detector, where it showed
	// up as a different sync test failing on almost every run.
	if !WaitFor(20*time.Second, func() bool {
		p1, err1 := td.Daemon.Store.GetPeer(other.NodeID())
		p2, err2 := other.Daemon.Store.GetPeer(td.NodeID())
		return err1 == nil && err2 == nil &&
			p1.Status == "online" && p2.Status == "online"
	}) {
		p1, _ := td.Daemon.Store.GetPeer(other.NodeID())
		p2, _ := other.Daemon.Store.GetPeer(td.NodeID())
		td.T.Fatalf("pairing did not come online on both sides (%s=%q, %s=%q)",
			td.Addr, p1.Status, other.Addr, p2.Status)
	}
}

// WriteSave writes a file into this daemon's save dir.
func (td *TestDaemon) WriteSave(rel, content string) {
	td.T.Helper()
	full := filepath.Join(td.SaveDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		td.T.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o666); err != nil {
		td.T.Fatal(err)
	}
}

// RemoveSave deletes a file from this daemon's save dir, as a game or a user
// would. Missing is not an error: the point is the file being gone.
func (td *TestDaemon) RemoveSave(rel string) {
	td.T.Helper()
	if err := os.Remove(filepath.Join(td.SaveDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
		td.T.Fatal(err)
	}
}

// ReadSave reads a file from the save dir ("" if missing).
func (td *TestDaemon) ReadSave(rel string) string {
	raw, err := os.ReadFile(filepath.Join(td.SaveDir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(raw)
}

// TrackGame tracks the daemon's save dir under the given name and returns
// the game id.
func (td *TestDaemon) TrackGame(name string) string {
	td.T.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	td.API(http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": td.SaveDir}, &resp)
	if resp.ID == "" {
		td.T.Fatalf("tracking %q returned no id", name)
	}
	return resp.ID
}

// WaitFor polls cond until true or timeout.
func WaitFor(timeout time.Duration, cond func() bool) bool {
	// Scaled for instrumented runs; see TimeoutScale.
	deadline := time.Now().Add(timeout * TimeoutScale)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return cond()
}

// Fmt is a tiny helper for readable failure messages.
func Fmt(format string, args ...any) string { return fmt.Sprintf(format, args...) }
