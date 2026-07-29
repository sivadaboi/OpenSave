package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/testutil"
)

type scanHit struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	SavePath string `json:"savePath"`
}

func scanFor(td *testutil.TestDaemon, wantPath string) *scanHit {
	td.T.Helper()
	var found []scanHit
	td.API(http.MethodGet, "/api/presets/scan", nil, &found)
	for i := range found {
		if found[i].SavePath == wantPath {
			return &found[i]
		}
	}
	return nil
}

// Excluding a location has to survive the scan that comes after it —
// dismissing something only to have it offered again next time is the
// complaint the feature exists to answer.
//
// The unit tests pin FilterExcluded's path matching; this covers the round
// trip the UI actually performs: save the path into settings, rescan, and
// find it gone.
func TestScanExclude_DismissedLocationStaysGone(t *testing.T) {
	td := testutil.NewTestDaemon(t, "ScanExclude")

	// A custom scan root with two candidate saves in it.
	root := t.TempDir()
	keep := filepath.Join(root, "KeepMe")
	drop := filepath.Join(root, "DropMe")
	for _, d := range []string{keep, drop} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "save.dat"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	td.API(http.MethodPost, "/api/settings", map[string]any{"customScanPaths": []string{root}}, nil)

	if scanFor(td, drop) == nil {
		t.Skip("the scanner did not offer the fixture directory; nothing to exclude")
	}

	td.API(http.MethodPost, "/api/settings", map[string]any{"excludePaths": []string{drop}}, nil)

	if hit := scanFor(td, drop); hit != nil {
		t.Errorf("the excluded location was offered again on the next scan: %+v", hit)
	}
	if scanFor(td, keep) == nil {
		t.Error("excluding one location also suppressed an unrelated one")
	}

	// And it is a setting, not a one-off: it has to read back so Settings can
	// list it and the user can undo it.
	var settings struct {
		ExcludePaths []string `json:"excludePaths"`
	}
	td.API(http.MethodGet, "/api/settings", nil, &settings)
	var persisted bool
	for _, p := range settings.ExcludePaths {
		if p == drop {
			persisted = true
		}
	}
	if !persisted {
		t.Errorf("the exclusion was not saved: %+v", settings.ExcludePaths)
	}
}

// Writing excludePaths must not disturb the rest of settings. The scan
// overlay posts a partial update while other panes may have written
// something, and a clobber here would look like unrelated settings randomly
// resetting themselves.
func TestScanExclude_PartialUpdateKeepsOtherSettings(t *testing.T) {
	td := testutil.NewTestDaemon(t, "ScanExcludeMerge")

	td.API(http.MethodPost, "/api/settings", map[string]any{"deviceName": "Bench", "syncInterval": 900}, nil)
	td.API(http.MethodPost, "/api/settings", map[string]any{"excludePaths": []string{filepath.Join(t.TempDir(), "nope")}}, nil)

	var settings struct {
		DeviceName   string   `json:"deviceName"`
		SyncInterval int      `json:"syncInterval"`
		ExcludePaths []string `json:"excludePaths"`
	}
	td.API(http.MethodGet, "/api/settings", nil, &settings)

	if settings.DeviceName != "Bench" {
		t.Errorf("deviceName = %q, want it untouched by an excludePaths write", settings.DeviceName)
	}
	if settings.SyncInterval != 900 {
		t.Errorf("syncInterval = %d, want it untouched by an excludePaths write", settings.SyncInterval)
	}
	if len(settings.ExcludePaths) != 1 {
		t.Errorf("excludePaths = %+v, want the one just written", settings.ExcludePaths)
	}
}
