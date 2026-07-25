package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/config"
)

// Commands that touch peers, pairing or syncing need the *running* daemon:
// that state lives in its process, not the database. Those talk to its local
// HTTP API. Purely local operations (scan, add, snapshot) keep working
// directly against the database, so they still function with no daemon up.

var httpClient = &http.Client{Timeout: 30 * time.Second}

// daemonBaseURL finds the running daemon via the address it publishes on
// start. Falls back to the configured port, which covers a daemon started by
// an older build that didn't publish one.
func daemonBaseURL() (string, error) {
	paths, err := config.Resolve()
	if err != nil {
		return "", err
	}
	if raw, err := os.ReadFile(filepath.Join(paths.HomeDir, "daemon.addr")); err == nil {
		if addr := strings.TrimSpace(string(raw)); addr != "" {
			return "http://" + addr, nil
		}
	}
	return "http://127.0.0.1:8383", nil
}

// daemonRequest calls the running daemon. The error text is deliberately
// actionable: "connection refused" on its own sends people hunting.
func daemonRequest(method, path string, body any) ([]byte, error) {
	base, err := daemonBaseURL()
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf(
			"the OpenSave daemon isn't reachable at %s — start it with `opensave daemon start` (or `systemctl --user start opensave-daemon`)",
			base)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errBody) == nil && errBody.Error != "" {
			return nil, fmt.Errorf("%s", errBody.Error)
		}
		return nil, fmt.Errorf("%s %s failed (%d)", method, path, resp.StatusCode)
	}
	return raw, nil
}

// daemonRunning reports whether a daemon is answering.
func daemonRunning() bool {
	base, err := daemonBaseURL()
	if err != nil {
		return false
	}
	quick := &http.Client{Timeout: 2 * time.Second}
	resp, err := quick.Get(base + "/api/status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ── Output helpers ───────────────────────────────────────────────────────
// Every command can emit JSON so the CLI is scriptable — the point of a
// headless client is being driven by something other than a human.

// jsonFlag reports whether --json was passed, and returns the remaining args.
func jsonFlag(args []string) (bool, []string) {
	out := args[:0:0]
	found := false
	for _, a := range args {
		if a == "--json" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return found, out
}

// emitJSON prints a value as indented JSON.
func emitJSON(v any) int {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Println(string(raw))
	return 0
}

// emitRawJSON prints a daemon response that is already JSON.
func emitRawJSON(raw []byte) int {
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		fmt.Println(pretty.String())
		return 0
	}
	fmt.Println(string(raw))
	return 0
}

// fail prints an error in the requested format and returns exit code 1.
func fail(asJSON bool, err error) int {
	if asJSON {
		emitJSON(map[string]string{"error": err.Error()})
		return 1
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 1
}
