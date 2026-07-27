package syncengine

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// TestDetectConflictIgnoresDirOnlyDivergence pins the reason conflicts were
// being raised over nothing. ManifestHash covers directories as well as
// files, so a folder present on one side only makes the hashes differ — and
// under a recorded merge-base that reads as "both sides changed", the exact
// shape of a real conflict. Nothing about the save disagrees, though: the
// modal has no differing file to name, and both sides report the same file
// count, bytes and mtime, so it asks the user to choose between two columns
// it draws identically.
func TestDetectConflictIgnoresDirOnlyDivergence(t *testing.T) {
	local := manifest(1000, map[string]delta.FileEntry{"save.dat": fileEntry("h1", 1000)}, "screenshots")
	remote := manifest(1000, map[string]delta.FileEntry{"save.dat": fileEntry("h1", 1000)})

	if local.ManifestHash() == remote.ManifestHash() {
		t.Fatal("precondition: a dir-only difference must still change the manifest hash")
	}

	// Under a merge-base neither side matches — the case that produced the
	// empty conflict modal.
	if DetectConflict(local, remote, 500, "some-older-agreed-hash") {
		t.Error("a folder on one side only must not be a conflict")
	}
	// And under the mtime fallback, with both sides modified since the sync.
	if DetectConflict(local, remote, 500, "") {
		t.Error("a folder on one side only must not be a conflict via the mtime path")
	}
}

// TestDetectConflictStillCatchesFileDivergence guards the fix from going too
// far: the dir carve-out must not swallow a genuine both-sides-changed save.
func TestDetectConflictStillCatchesFileDivergence(t *testing.T) {
	local := manifest(3000, map[string]delta.FileEntry{"save.dat": fileEntry("local-h", 3000)}, "shared")
	remote := manifest(3000, map[string]delta.FileEntry{"save.dat": fileEntry("remote-h", 3000)}, "shared")

	if !DetectConflict(local, remote, 500, "agreed-base") {
		t.Error("differing content for the same file is a real conflict")
	}

	// A file on one side only also counts as a content difference.
	extra := manifest(3000, map[string]delta.FileEntry{
		"save.dat": fileEntry("h1", 3000),
		"extra.sv": fileEntry("h2", 3000),
	})
	base := manifest(3000, map[string]delta.FileEntry{"save.dat": fileEntry("h1", 3000)})
	if !DetectConflict(extra, base, 500, "agreed-base") {
		t.Error("a file present on one side only is a content difference")
	}
}

// TestDiffManifestsListsDirs covers the other half of the report: when a
// conflict is real, the modal must be able to account for every part of it,
// directories included, rather than silently listing files only.
func TestDiffManifestsListsDirs(t *testing.T) {
	local := manifest(1000, map[string]delta.FileEntry{"save.dat": fileEntry("h1", 1000)}, "shared", "only-here")
	remote := manifest(1000, map[string]delta.FileEntry{"save.dat": fileEntry("h2", 1000)}, "shared", "only-there")

	diffs := diffManifests(local, remote)

	byPath := map[string]DiffFile{}
	for _, d := range diffs {
		byPath[d.Path] = d
	}
	if got := byPath["save.dat"].Status; got != "changed" {
		t.Errorf("save.dat status = %q, want changed", got)
	}
	if got := byPath["only-here/"].Status; got != "only-local" {
		t.Errorf("only-here/ status = %q, want only-local", got)
	}
	if got := byPath["only-there/"].Status; got != "only-remote" {
		t.Errorf("only-there/ status = %q, want only-remote", got)
	}
	if _, ok := byPath["shared/"]; ok {
		t.Error("a directory both sides have must not be listed as differing")
	}
	if len(diffs) != 3 {
		t.Errorf("diff count = %d, want 3 (one file, two dirs)", len(diffs))
	}
	// Sizes are meaningless for a folder; the UI renders -1 as "—".
	if byPath["only-here/"].LocalSize != -1 || byPath["only-here/"].RemoteSize != -1 {
		t.Error("directory entries should carry no sizes")
	}
}
