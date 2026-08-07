package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func twoRoots(t *testing.T) (primary, config string) {
	t.Helper()
	primary = t.TempDir()
	config = t.TempDir()
	writeFile(t, primary, "ER0000.sl2", "save data")
	writeFile(t, config, "settings.ini", "fullscreen=1")
	return primary, config
}

// The property the whole design rests on: a manifest's merge base covers the
// primary location and nothing else. Add a second location and the base is
// unchanged — so a device with extra roots still agrees with one without
// them, instead of reading as permanently diverged and conflicting on every
// sync over saves neither side touched.
func TestExtraRootsDoNotMoveTheMergeBase(t *testing.T) {
	primary, config := twoRoots(t)

	single, err := BuildManifest(primary)
	if err != nil {
		t.Fatal(err)
	}
	multi, failures, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected read failures: %v", failures)
	}
	if len(multi.Extra) != 1 {
		t.Fatalf("extra roots = %d, want 1", len(multi.Extra))
	}

	if multi.ManifestHash() != single.ManifestHash() {
		t.Error("adding a second save location changed the merge base; a peer without that location could never agree again")
	}

	// And changing the extra root still must not move it.
	writeFile(t, config, "settings.ini", "fullscreen=0")
	changed, _, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ManifestHash() != single.ManifestHash() {
		t.Error("editing a file in an extra location moved the primary location's merge base")
	}
}

// The watcher's question is different: has anything at all changed here.
// That one does have to span every location, or an edit to a config folder
// would never trigger a snapshot.
func TestContentHashSpansEveryRoot(t *testing.T) {
	primary, config := twoRoots(t)

	before, _, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, config, "settings.ini", "fullscreen=0")
	after, _, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}

	if before.ContentHash() == after.ContentHash() {
		t.Error("a change in an extra location left the content hash unmoved; no snapshot would ever be taken for it")
	}
	if before.ManifestHash() != after.ManifestHash() {
		t.Error("the merge base moved — see TestExtraRootsDoNotMoveTheMergeBase")
	}
}

// Existing games must not all appear changed the moment they upgrade: for a
// single-root game the two hashes are the same value, so the hash already
// stored against every tracked game stays valid.
func TestContentHashMatchesManifestHashWithoutExtraRoots(t *testing.T) {
	primary := t.TempDir()
	writeFile(t, primary, "ER0000.sl2", "save data")

	m, err := BuildManifest(primary)
	if err != nil {
		t.Fatal(err)
	}
	if m.ContentHash() != m.ManifestHash() {
		t.Error("a single-root game's content hash differs from its manifest hash; every existing game would look changed on upgrade")
	}
}

// A peer that predates multi-root decodes the manifest into a struct with no
// Extra field. It must still see the primary location exactly as before —
// and must NOT see extra-root files, which it would otherwise write as
// subfolders of the primary save.
func TestOldPeersSeeOnlyThePrimaryLocation(t *testing.T) {
	primary, config := twoRoots(t)
	multi, _, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}

	wire, err := json.Marshal(multi)
	if err != nil {
		t.Fatal(err)
	}

	// The shape a peer on an older build decodes into.
	var old struct {
		Files map[string]FileEntry `json:"files"`
		Dirs  []string             `json:"dirs"`
	}
	if err := json.Unmarshal(wire, &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Files) != 1 {
		t.Fatalf("an older peer sees %d files, want just the primary location's 1: %+v", len(old.Files), old.Files)
	}
	if _, ok := old.Files["ER0000.sl2"]; !ok {
		t.Error("an older peer lost the primary save file")
	}
	if _, ok := old.Files["settings.ini"]; ok {
		t.Error("an extra location's file leaked into the primary file list; an older peer would write it into the save folder")
	}
}

// Single-root games are almost every game, and their manifests must not grow
// a new field on the wire — an empty object still changes bytes, and this
// travels on every sync.
func TestSingleRootManifestIsUnchangedOnTheWire(t *testing.T) {
	primary := t.TempDir()
	writeFile(t, primary, "ER0000.sl2", "save data")
	m, err := BuildManifest(primary)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["extraRoots"]; ok {
		t.Error("a single-root manifest carries an extraRoots key; it should be omitted entirely")
	}
}

// A location that cannot be read is reported, not reported as empty. Empty
// would propagate to the peer as "every file here was deleted".
func TestUnreadableRootIsReportedRatherThanEmptied(t *testing.T) {
	primary, config := twoRoots(t)
	missing := filepath.Join(t.TempDir(), "not-here")

	m, failures, err := BuildMultiManifest(primary, map[string]string{
		"config": config,
		"mods":   missing,
	})
	if err != nil {
		t.Fatalf("one unreadable extra location aborted the whole manifest: %v", err)
	}
	if _, ok := failures["mods"]; !ok {
		t.Error("the unreadable location was not reported")
	}
	if _, ok := m.Extra["mods"]; ok {
		t.Error("the unreadable location appears in the manifest as empty; a peer would read that as every file in it being deleted")
	}
	if _, ok := m.Extra["config"]; !ok {
		t.Error("a readable location was dropped because a different one failed")
	}
	if len(m.Files) != 1 {
		t.Error("the primary location stopped syncing because an extra one was unreadable")
	}
}

func TestRootAccessors(t *testing.T) {
	primary, config := twoRoots(t)
	m, _, err := BuildMultiManifest(primary, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err)
	}

	names := m.RootNames()
	if len(names) != 2 || names[0] != PrimaryRoot || names[1] != "config" {
		t.Errorf("RootNames() = %q, want the primary first then the rest sorted", names)
	}
	if len(m.Root(PrimaryRoot).Files) != 1 {
		t.Error("Root(primary) did not return the primary files")
	}
	if _, ok := m.Root("config").Files["settings.ini"]; !ok {
		t.Error("Root(\"config\") did not return that location's files")
	}
	if len(m.Root("never-heard-of-it").Files) != 0 {
		t.Error("an unknown root should read as empty, not panic")
	}
}

// The merge base is a value every device has already stored. If its
// algorithm ever changes, every pair of devices in the world disagrees with
// every peer exactly once and conflicts on saves nobody touched — a silent,
// global version of the bug this project has fixed several times.
//
// legacyManifestHash is the algorithm as it stood before save locations
// became plural, transcribed from the committed version rather than
// refactored out of it, so this compares against history and not against
// itself.
func legacyManifestHash(m Manifest) string {
	paths := make([]string, 0, len(m.Files))
	for p := range m.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		io.WriteString(h, p)
		io.WriteString(h, ":")
		io.WriteString(h, m.Files[p].Hash)
		io.WriteString(h, "\n")
	}
	for _, d := range m.Dirs {
		io.WriteString(h, "dir:"+d+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestManifestHashIsUnchangedByMultiRoot(t *testing.T) {
	m := Manifest{
		Files: map[string]FileEntry{
			"ER0000.sl2":     {Hash: "aaa"},
			"sub/ER0001.sl2": {Hash: "bbb"},
		},
		Dirs: []string{"sub"},
	}

	want := legacyManifestHash(m)
	if got := m.ManifestHash(); got != want {
		t.Fatalf("ManifestHash no longer matches the algorithm devices already store:\n got  %s\n want %s", got, want)
	}

	// Adding a second location must leave it exactly where it was.
	m.Extra = map[string]RootManifest{
		"config": {Files: map[string]FileEntry{"settings.ini": {Hash: "ccc"}}},
	}
	if got := m.ManifestHash(); got != want {
		t.Errorf("an extra location moved the primary merge base to %s", got)
	}

	// And the extra location does have a base of its own, distinct from it.
	if got := m.RootHash("config"); got == want || got == "" {
		t.Errorf("the extra location's base is %q; it must be its own value", got)
	}
}
