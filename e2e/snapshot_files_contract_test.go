package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/testutil"
)

// Two bugs lived here, both a field name the CLI got wrong against this API,
// and neither could fail loudly:
//
//   - the listing sends "size"; the CLI read "sizeBytes", which unmarshals
//     to zero without error, so every snapshot reported every file as 0 B;
//   - restore-file wants "relPath"; the CLI sent "path", so single-file
//     restore returned "relPath is required" and restored nothing, every
//     time, for a feature the release notes advertise.
//
// Nothing mocked would have caught either: both sides were self-consistent
// and only disagreed with each other. So this pins the wire contract, using
// the exact field names the CLI puts on it.
func TestSnapshotFilesContract(t *testing.T) {
	td := testutil.NewTestDaemon(t, "SnapFilesContract")

	td.WriteSave("slot1.sav", "fifteen-bytes!!")
	if err := os.MkdirAll(filepath.Join(td.SaveDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	td.WriteSave("sub/deep.dat", "seven!!")
	gameID := td.TrackGame("Contract")

	var snap struct {
		ID string `json:"id"`
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "c"}, &snap)
	if snap.ID == "" {
		t.Fatal("snapshot returned no id")
	}

	// The listing must use "size", and it must be the real byte count — a
	// zero here is what made every save look empty.
	var files []struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}
	td.API(http.MethodGet, "/api/games/"+gameID+"/snapshot/"+snap.ID+"/files", nil, &files)

	var sawFile bool
	for _, f := range files {
		if f.Path == "slot1.sav" {
			sawFile = true
			if f.Size != 15 {
				t.Errorf("slot1.sav size = %d, want 15 — the listing field is %q", f.Size, "size")
			}
			if f.IsDir {
				t.Error("slot1.sav reported as a directory")
			}
		}
	}
	if !sawFile {
		t.Fatalf("the snapshot listing did not include slot1.sav: %+v", files)
	}

	// restore-file takes relPath. Prove the wrong name is rejected rather
	// than silently doing nothing, so a future rename cannot pass quietly.
	td.WriteSave("slot1.sav", "WRECKED")
	status := td.APIStatus(http.MethodPost,
		"/api/games/"+gameID+"/snapshot/"+snap.ID+"/restore-file",
		map[string]string{"path": "slot1.sav"}, nil)
	if status == http.StatusOK {
		t.Error(`restore-file accepted "path" — if it now takes either name, the CLI's ` +
			`payload is fine, but this test is no longer pinning anything`)
	}
	if got := td.ReadSave("slot1.sav"); got != "WRECKED" {
		t.Errorf("a rejected restore still wrote the file: %q", got)
	}

	// And the right name restores.
	td.API(http.MethodPost,
		"/api/games/"+gameID+"/snapshot/"+snap.ID+"/restore-file",
		map[string]string{"relPath": "slot1.sav"}, nil)
	if got := td.ReadSave("slot1.sav"); got != "fifteen-bytes!!" {
		t.Errorf("slot1.sav = %q, want it restored from the snapshot", got)
	}
}
