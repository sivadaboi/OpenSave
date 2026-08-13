package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

func manifestOf(paths ...string) delta.Manifest {
	m := delta.Manifest{Files: map[string]delta.FileEntry{}}
	for _, p := range paths {
		m.Files[p] = delta.FileEntry{Hash: "h-" + p}
	}
	return m
}

// Risk is about what leaves, not what arrives. Getting this wrong in the
// permissive direction loses saves; getting it wrong in the strict direction
// stops syncing for reasons the user cannot see, which is how a file that had
// never been on this device failed to arrive because a different folder had
// been edited.
func TestFilesAtRisk(t *testing.T) {
	local := manifestOf("save.sav", "settings.ini")

	cases := []struct {
		name string
		d    Decision
		want []string
	}{
		{
			name: "a file this device never held is not at risk",
			d:    Decision{FilesToPull: []string{"brand-new.sav"}},
			want: nil,
		},
		{
			name: "a file being written over is at risk",
			d:    Decision{FilesToPull: []string{"save.sav"}},
			want: []string{"save.sav"},
		},
		{
			name: "only the held ones, out of a mixed pull",
			d:    Decision{FilesToPull: []string{"brand-new.sav", "save.sav"}},
			want: []string{"save.sav"},
		},
		{
			name: "a deletion arriving from the peer is at risk too",
			d:    Decision{FilesToDeleteLocally: []string{"settings.ini"}},
			want: []string{"settings.ini"},
		},
		{
			name: "a deletion of something already gone is not",
			d:    Decision{FilesToDeleteLocally: []string{"never-here.sav"}},
			want: nil,
		},
		{
			name: "pushing risks nothing locally",
			d:    Decision{FilesToPush: []string{"save.sav"}, FilesToDeleteOnPeer: []string{"settings.ini"}},
			want: nil,
		},
		{
			name: "nothing happening risks nothing",
			d:    Decision{},
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filesAtRisk(local, c.d)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestFilesAtRisk_EmptyLocalSaveRisksNothing(t *testing.T) {
	if got := filesAtRisk(delta.Manifest{}, Decision{FilesToPull: []string{"anything.sav"}}); got != nil {
		t.Errorf("got %v; a device holding no files has nothing to lose", got)
	}
}

// The protection itself: before a sync writes over a file this device already
// holds, the old contents must be recoverable.
func TestSync_SnapshotsBeforeOverwritingALocalFile(t *testing.T) {
	env := setupEngine(t)
	env.engine.Log = func(level, msg string) { t.Logf("[%s] %s", level, msg) }

	// Reach agreement first. Two sides holding different content for the same
	// file is a genuine conflict and is refused further up — that is correct,
	// and it is not the case this guards. The case that matters is the
	// ordinary one: both agreed, then the peer moved on, and this device's
	// copy is about to be written over.
	write(t, env.localDir, "save.dat", "agreed contents")
	write(t, env.remoteDir, "save.dat", "agreed contents")
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("establishing agreement: %v", err)
	}

	write(t, env.remoteDir, "save.dat", "the peer's newer copy")

	g, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	before, err := env.store.ListSnapshots("game1", g.ActiveBranch)
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("SyncWithPeer: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	after, err := env.store.ListSnapshots("game1", g.ActiveBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) <= len(before) {
		t.Fatalf("local file was replaced with no snapshot taken first — the old contents are gone.\n"+
			"  sync result: %+v\n  local file now: %q\n  snapshots: %d -> %d",
			res, got, len(before), len(after))
	}
}

// And it must not fire when nothing is being replaced, or every ordinary sync
// that brings a new file would leave a snapshot behind and churn the history
// the user actually wants to look at.
func TestSync_NoSnapshotWhenNothingIsReplaced(t *testing.T) {
	env := setupEngine(t)
	write(t, env.remoteDir, "brand-new.dat", "a file this device has never held")

	g, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	before, err := env.store.ListSnapshots("game1", g.ActiveBranch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("SyncWithPeer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.localDir, "brand-new.dat")); err != nil {
		t.Fatalf("the new file should still have arrived: %v", err)
	}

	after, err := env.store.ListSnapshots("game1", g.ActiveBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("a snapshot was taken for a sync that replaced nothing (%d -> %d)", len(before), len(after))
	}
}
