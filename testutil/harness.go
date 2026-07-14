// Package testutil provides the E2E harness: full in-process daemons with
// isolated data dirs and real HTTP servers on loopback, so tests exercise
// the same wire protocol two real devices would.
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
}

// NewTestDaemon boots a daemon with an isolated home dir and API server on
// an OS-assigned port. Discovery is disabled; tests pair explicitly.
func NewTestDaemon(t *testing.T, name string) *TestDaemon {
	t.Helper()

	home := t.TempDir()
	d, err := daemon.New(daemon.Options{HomeOverride: home, DisableDiscovery: true})
	if err != nil {
		t.Fatalf("daemon.New(%s): %v", name, err)
	}

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

	saveDir := filepath.Join(t.TempDir(), "saves")
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
	status := td.apiRaw(method, path, body, out)
	if status >= 400 {
		td.T.Fatalf("%s %s -> %d", method, path, status)
	}
}

// APIStatus performs a request and returns the HTTP status without failing
// the test — for calls that may legitimately return a 4xx.
func (td *TestDaemon) APIStatus(method, path string, body any, out any) int {
	td.T.Helper()
	return td.apiRaw(method, path, body, out)
}

func (td *TestDaemon) apiRaw(method, path string, body any, out any) int {
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
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
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

	// Both sides must now know each other.
	if !WaitFor(10*time.Second, func() bool {
		_, err1 := td.Daemon.Store.GetPeer(other.NodeID())
		_, err2 := other.Daemon.Store.GetPeer(td.NodeID())
		return err1 == nil && err2 == nil
	}) {
		td.T.Fatal("pairing did not complete on both sides")
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
	deadline := time.Now().Add(timeout)
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
