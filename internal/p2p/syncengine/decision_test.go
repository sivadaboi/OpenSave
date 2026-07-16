package syncengine

import (
	"reflect"
	"sort"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

func manifest(latestMtime int64, files map[string]delta.FileEntry, dirs ...string) delta.Manifest {
	if files == nil {
		files = map[string]delta.FileEntry{}
	}
	return delta.Manifest{LatestMtime: delta.Milli(latestMtime), Files: files, Dirs: dirs}
}

func fileEntry(hash string, mtime int64) delta.FileEntry {
	return delta.FileEntry{Hash: hash, MtimeMs: delta.Milli(mtime), BlockSize: 65536}
}

func set(items ...string) map[string]struct{} {
	s := map[string]struct{}{}
	for _, i := range items {
		s[i] = struct{}{}
	}
	return s
}

func TestDetectConflict(t *testing.T) {
	cases := []struct {
		name       string
		local      delta.Manifest
		remote     delta.Manifest
		lastSyncMs int64
		want       bool
	}{
		{
			name:   "identical manifests never conflict",
			local:  manifest(9000, map[string]delta.FileEntry{"a": fileEntry("h1", 9000)}),
			remote: manifest(9999, map[string]delta.FileEntry{"a": fileEntry("h1", 9999)}),
			want:   false,
		},
		{
			name:       "never synced, both sides have files -> conflict",
			local:      manifest(1000, map[string]delta.FileEntry{"a": fileEntry("h1", 1000)}),
			remote:     manifest(2000, map[string]delta.FileEntry{"a": fileEntry("h2", 2000)}),
			lastSyncMs: 0,
			want:       true,
		},
		{
			name:       "never synced, local empty -> no conflict (fresh device)",
			local:      manifest(0, nil),
			remote:     manifest(2000, map[string]delta.FileEntry{"a": fileEntry("h2", 2000)}),
			lastSyncMs: 0,
			want:       false,
		},
		{
			name:       "both modified after last sync -> conflict",
			local:      manifest(20000, map[string]delta.FileEntry{"a": fileEntry("h1", 20000)}),
			remote:     manifest(25000, map[string]delta.FileEntry{"a": fileEntry("h2", 25000)}),
			lastSyncMs: 10000,
			want:       true,
		},
		{
			name:       "only remote modified after last sync -> plain pull, no conflict",
			local:      manifest(5000, map[string]delta.FileEntry{"a": fileEntry("h1", 5000)}),
			remote:     manifest(25000, map[string]delta.FileEntry{"a": fileEntry("h2", 25000)}),
			lastSyncMs: 10000,
			want:       false,
		},
		{
			name: "clock skew within 2s tolerance does not count as modified",
			// local mtime is 1.5s past lastSync — inside the tolerance window.
			local:      manifest(11500, map[string]delta.FileEntry{"a": fileEntry("h1", 11500)}),
			remote:     manifest(25000, map[string]delta.FileEntry{"a": fileEntry("h2", 25000)}),
			lastSyncMs: 10000,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectConflict(tc.local, tc.remote, tc.lastSyncMs, ""); got != tc.want {
				t.Errorf("DetectConflict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompute_LineageClassification(t *testing.T) {
	local := manifest(0, map[string]delta.FileEntry{
		"unchanged.dat":     fileEntry("same", 100),
		"local-new.dat":     fileEntry("ln", 100), // not in lineage -> push
		"peer-deleted.dat":  fileEntry("pd", 100), // in lineage, gone on remote -> delete locally
		"both-newer-local":  fileEntry("l2", 200), // differs, local newer -> push
		"both-newer-remote": fileEntry("l1", 100), // differs, remote newer -> pull
		"tie.dat":           fileEntry("l1", 150), // differs, equal mtime -> pull (tie-break)
	}, "keep-dir", "local-new-dir", "peer-deleted-dir")

	remote := manifest(0, map[string]delta.FileEntry{
		"unchanged.dat":     fileEntry("same", 999),
		"remote-new.dat":    fileEntry("rn", 100), // not in lineage -> pull
		"local-deleted.dat": fileEntry("ld", 100), // in lineage, gone locally -> delete on peer
		"both-newer-local":  fileEntry("r1", 100),
		"both-newer-remote": fileEntry("r2", 200),
		"tie.dat":           fileEntry("r1", 150),
	}, "keep-dir", "remote-new-dir", "local-deleted-dir")

	lineageFiles := set("unchanged.dat", "peer-deleted.dat", "local-deleted.dat",
		"both-newer-local", "both-newer-remote", "tie.dat")
	lineageDirs := set("keep-dir", "peer-deleted-dir", "local-deleted-dir")

	d := Compute(local, remote, lineageFiles, lineageDirs)

	check := func(name string, got, want []string) {
		t.Helper()
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	check("FilesToPull", d.FilesToPull, []string{"remote-new.dat", "both-newer-remote", "tie.dat"})
	check("FilesToPush", d.FilesToPush, []string{"local-new.dat", "both-newer-local"})
	check("FilesToDeleteLocally", d.FilesToDeleteLocally, []string{"peer-deleted.dat"})
	check("FilesToDeleteOnPeer", d.FilesToDeleteOnPeer, []string{"local-deleted.dat"})
	check("DirsToPull", d.DirsToPull, []string{"remote-new-dir"})
	check("DirsToPush", d.DirsToPush, []string{"local-new-dir"})
	check("DirsToDeleteLocally", d.DirsToDeleteLocally, []string{"peer-deleted-dir"})
	check("DirsToDeleteOnPeer", d.DirsToDeleteOnPeer, []string{"local-deleted-dir"})
}

func TestCompute_InSync(t *testing.T) {
	files := map[string]delta.FileEntry{"a": fileEntry("h", 100)}
	d := Compute(manifest(0, files, "d1"), manifest(0, files, "d1"), set("a"), set("d1"))
	if d.HasChanges() {
		t.Errorf("identical manifests should produce no changes: %+v", d)
	}
}

func TestDifferentBlockIndices(t *testing.T) {
	remote := delta.FileEntry{
		BlockSize: 65536,
		Blocks: []delta.Block{
			{Index: 0, Hash: "a"},
			{Index: 1, Hash: "b"},
			{Index: 2, Hash: "c"},
		},
	}

	// New file locally: all blocks.
	if got := DifferentBlockIndices(nil, remote); len(got) != 3 {
		t.Errorf("nil local should fetch all blocks, got %v", got)
	}

	// Block size mismatch: all blocks.
	mismatch := &delta.FileEntry{BlockSize: 512 * 1024, Blocks: []delta.Block{{Index: 0, Hash: "a"}}}
	if got := DifferentBlockIndices(mismatch, remote); len(got) != 3 {
		t.Errorf("block size mismatch should fetch all blocks, got %v", got)
	}

	// Partial change: only differing/new indices.
	partial := &delta.FileEntry{BlockSize: 65536, Blocks: []delta.Block{
		{Index: 0, Hash: "a"},       // same
		{Index: 1, Hash: "CHANGED"}, // differs
	}}
	got := DifferentBlockIndices(partial, remote)
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("partial diff = %v, want [1 2]", got)
	}

	// File shrank remotely: local has more blocks, nothing extra to fetch.
	longer := &delta.FileEntry{BlockSize: 65536, Blocks: []delta.Block{
		{Index: 0, Hash: "a"}, {Index: 1, Hash: "b"}, {Index: 2, Hash: "c"}, {Index: 3, Hash: "d"},
	}}
	if got := DifferentBlockIndices(longer, remote); len(got) != 0 {
		t.Errorf("shrunk remote file should fetch nothing, got %v", got)
	}
}

func TestBatchIndices(t *testing.T) {
	indices := make([]int, 40)
	for i := range indices {
		indices[i] = i
	}

	// 64KB blocks: 1.5MB/64KB = 24, capped at 16 LAN / 8 WAN.
	lan := BatchIndices(indices, 65536, false)
	if len(lan[0]) != 16 {
		t.Errorf("LAN batch size = %d, want 16", len(lan[0]))
	}
	wan := BatchIndices(indices, 65536, true)
	if len(wan[0]) != 8 {
		t.Errorf("WAN batch size = %d, want 8", len(wan[0]))
	}

	// 2MB blocks: 1.5MB/2MB = 0 -> floor of 1 per batch.
	big := BatchIndices([]int{0, 1, 2}, 2*1024*1024, false)
	if len(big) != 3 || len(big[0]) != 1 {
		t.Errorf("oversized blocks should batch one at a time, got %v", big)
	}

	if ConcurrencyFor(true) != 3 || ConcurrencyFor(false) != 5 {
		t.Error("concurrency constants wrong")
	}
}

// TestIntersectLineage_UnconfirmedPushNeverEntersLineage is the regression
// test for a real data-loss incident: device A pushed a file, the peer's
// pull kept failing (AV lock on a fresh .exe), and A recorded the file as
// "synced" anyway from its local manifest alone. On the next run "in
// lineage but missing on peer" was read as "peer deleted it" — and A
// deleted the user's original. Lineage must be the manifest intersection:
// a pushed file the peer never received stays out, so the next sync
// re-pushes it instead of deleting it.
func TestIntersectLineage_UnconfirmedPushNeverEntersLineage(t *testing.T) {
	local := delta.Manifest{
		Files: map[string]delta.FileEntry{
			"shared.sav":   {Hash: "h1"},
			"OpenSave.exe": {Hash: "h2"}, // pushed, peer's pull failed
		},
		Dirs: []string{"common", "local-only"},
	}
	remote := delta.Manifest{
		Files: map[string]delta.FileEntry{
			"shared.sav": {Hash: "h1"},
		},
		Dirs: []string{"common"},
	}

	files, dirs := IntersectLineage(local, remote)
	if len(files) != 1 || files[0] != "shared.sav" {
		t.Fatalf("lineage files = %v, want only shared.sav", files)
	}
	if len(dirs) != 1 || dirs[0] != "common" {
		t.Fatalf("lineage dirs = %v, want only common", dirs)
	}

	// And the decision that follows from the corrected lineage: the file
	// missing on the remote is re-PUSHED, never deleted locally.
	d := Compute(local, remote, toSet(files), toSet(dirs))
	if len(d.FilesToDeleteLocally) != 0 {
		t.Fatalf("pushed-but-unreceived file must never be deleted locally, got %v", d.FilesToDeleteLocally)
	}
	if len(d.FilesToPush) != 1 || d.FilesToPush[0] != "OpenSave.exe" {
		t.Fatalf("expected OpenSave.exe re-push, got %v", d.FilesToPush)
	}
}

// TestDetectConflict_ContentBased pins the merge-base semantics: with an
// agreed hash recorded, conflicts are purely content-based — no clocks.
func TestDetectConflict_ContentBased(t *testing.T) {
	m := func(name, content string) delta.Manifest {
		return delta.Manifest{Files: map[string]delta.FileEntry{
			name: {Hash: content},
		}}
	}
	base := m("save.dat", "v1")
	agreed := base.ManifestHash()

	localChanged := m("save.dat", "v2-local")
	remoteChanged := m("save.dat", "v2-remote")

	cases := []struct {
		name          string
		local, remote delta.Manifest
		want          bool
	}{
		{"identical never conflicts", localChanged, localChanged, false},
		{"only local changed — one-sided, no conflict", localChanged, base, false},
		{"only remote changed — one-sided, no conflict", base, remoteChanged, false},
		{"both changed — conflict regardless of mtimes", localChanged, remoteChanged, true},
	}
	for _, tc := range cases {
		// lastSyncMs deliberately absurd (future) to prove clocks are
		// ignored when an agreed hash exists.
		if got := DetectConflict(tc.local, tc.remote, 1<<62, agreed); got != tc.want {
			t.Errorf("%s: DetectConflict = %v, want %v", tc.name, got, tc.want)
		}
	}
}
