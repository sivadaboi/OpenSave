package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildNAND writes the yuzu-lineage save layout:
//
//	<root>/<account>/<profile-uuid>/<title-id>/<file>
func buildNAND(t *testing.T, root, profile string, titles ...string) {
	t.Helper()
	for _, title := range titles {
		d := filepath.Join(root, "0000000000000000", profile, title)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "save.dat"), []byte("save"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Reported by a Steam Deck user paired with a PC: Eden refusing the synced
// save with "there's a save file without an associated profile", the two
// devices' profile ids being "a different string of numbers".
//
// That is the emulator's error, not a sync error, and it is caused by what we
// offer to track. The save root holds <account>/<profile-uuid>/<title-id>/,
// and the profile uuid is generated per install — so tracking the root syncs
// one machine's uuid folder onto another machine that has a different one,
// and its emulator finds a save belonging to nobody.
//
// The title folder is the part both installs agree on.
func TestSwitchNANDTitlesSkipsTheProfileID(t *testing.T) {
	root := t.TempDir()
	const profile = "8f3a1b2c4d5e6f708192a3b4c5d6e7f8"
	buildNAND(t, root, profile, "0100000000010000", "01006A800016E000")

	titles := switchNANDTitles(root)
	if len(titles) != 2 {
		t.Fatalf("found %d title folders, want 2: %v", len(titles), titles)
	}
	for _, got := range titles {
		if strings.Contains(filepath.Base(got), profile) {
			t.Errorf("returned the profile folder itself: %s", got)
		}
		// The uuid is still in the absolute path — it has to be, that is where
		// the files live. What matters is that it is the tracked folder's
		// parent rather than something inside the folder, so it never travels.
		if filepath.Base(filepath.Dir(got)) != profile {
			t.Errorf("%s is not directly inside the profile folder", got)
		}
	}
}

// Two installs of the same game under different profile ids must produce
// title folders whose *contents* line up, which is the whole point: the same
// relative tree on both ends.
func TestSwitchNANDTitlesAgreeAcrossProfiles(t *testing.T) {
	pc, deck := t.TempDir(), t.TempDir()
	const title = "0100000000010000"
	buildNAND(t, pc, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", title)
	buildNAND(t, deck, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", title)

	pcTitles, deckTitles := switchNANDTitles(pc), switchNANDTitles(deck)
	if len(pcTitles) != 1 || len(deckTitles) != 1 {
		t.Fatalf("expected one title each, got %v and %v", pcTitles, deckTitles)
	}
	if filepath.Base(pcTitles[0]) != filepath.Base(deckTitles[0]) {
		t.Errorf("the two devices offer different folder names (%s vs %s) — "+
			"syncing them would not line up",
			filepath.Base(pcTitles[0]), filepath.Base(deckTitles[0]))
	}
}

// Anything that is not a title id must be left alone: emulator working dirs
// and user backups sit beside the real saves, and offering them as games is
// how the scan results became unmanageable in the first place.
func TestSwitchNANDTitlesIgnoresNonTitleFolders(t *testing.T) {
	root := t.TempDir()
	const profile = "8f3a1b2c4d5e6f708192a3b4c5d6e7f8"
	buildNAND(t, root, profile, "0100000000010000")

	base := filepath.Join(root, "0000000000000000", profile)
	for _, junk := range []string{"backup", "temp", "0100", "not-hex-at-all-xx", "0100000000010000-old"} {
		d := filepath.Join(base, junk)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f.dat"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	titles := switchNANDTitles(root)
	if len(titles) != 1 {
		t.Errorf("found %d entries, want only the real title id: %v", len(titles), titles)
	}
}

// An empty title folder is created before a game has ever saved. Offering it
// would put an empty entry in the scan results for every game ever launched.
func TestSwitchNANDTitlesSkipsEmptyTitles(t *testing.T) {
	root := t.TempDir()
	const profile = "8f3a1b2c4d5e6f708192a3b4c5d6e7f8"
	buildNAND(t, root, profile, "0100000000010000")
	if err := os.MkdirAll(filepath.Join(root, "0000000000000000", profile, "0100000000029999"), 0o755); err != nil {
		t.Fatal(err)
	}

	titles := switchNANDTitles(root)
	if len(titles) != 1 {
		t.Errorf("found %d entries, want only the one with a save in it: %v", len(titles), titles)
	}
}

// A root that is not this layout must yield nothing rather than guessing.
func TestSwitchNANDTitlesToleratesOtherLayouts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "some", "other", "shape"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := switchNANDTitles(root); len(got) != 0 {
		t.Errorf("an unrelated layout produced %v", got)
	}
	if got := switchNANDTitles(filepath.Join(root, "does-not-exist")); len(got) != 0 {
		t.Errorf("a missing root produced %v", got)
	}
}

func TestIsSwitchTitleID(t *testing.T) {
	for _, ok := range []string{"0100000000010000", "01006A800016E000", "abcdef0123456789", "ABCDEF0123456789"} {
		if !isSwitchTitleID(ok) {
			t.Errorf("%q rejected, want accepted", ok)
		}
	}
	for _, bad := range []string{"", "0100", "0100000000010000extra", "g100000000010000", "0100-0000-0001", "backup"} {
		if isSwitchTitleID(bad) {
			t.Errorf("%q accepted, want rejected", bad)
		}
	}
}

// The layout varies between forks and versions, and a guess that misses would
// remove detection altogether — a worse outcome than the profile-id problem
// the per-title split exists to avoid. So an unrecognised shape must still be
// offered as the save root, exactly as before.
func TestSwitchNANDFallsBackToTheRootWhenTheLayoutIsUnfamiliar(t *testing.T) {
	home := t.TempDir()
	// One level below save/, not the three the NAND layout has.
	save := filepath.Join(home, ".local", "share", "eden", "nand", "user", "save", "0000")
	if err := os.MkdirAll(save, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "save.bin"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	sc := &Scanner{CacheFile: filepath.Join(t.TempDir(), "cache.json"), GOOS: "linux", HomeDir: home}
	var found bool
	for _, d := range sc.Scan(nil) {
		if d.ID == "eden" && d.Name == "Eden Switch Emulator" {
			found = true
		}
	}
	if !found {
		t.Error("an unrecognised NAND layout stopped being detected at all")
	}
}

// And when the layout IS the real one, the per-title entries replace the root
// rather than appearing alongside it — offering both would have the user
// tracking the same saves twice, once with the profile id inside.
func TestSwitchNANDOffersTitlesInsteadOfTheRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "eden", "nand", "user", "save")
	buildNAND(t, root, "8f3a1b2c4d5e6f708192a3b4c5d6e7f8", "0100000000010000")

	sc := &Scanner{CacheFile: filepath.Join(t.TempDir(), "cache.json"), GOOS: "linux", HomeDir: home}
	var rootOffered, titleOffered bool
	for _, d := range sc.Scan(nil) {
		switch {
		case d.ID == "eden":
			rootOffered = true
		case strings.HasPrefix(d.ID, "eden-"):
			titleOffered = true
			if filepath.Base(d.SavePath) != "0100000000010000" {
				t.Errorf("title entry points at %s", d.SavePath)
			}
		}
	}
	if !titleOffered {
		t.Error("the per-title entry was not offered")
	}
	if rootOffered {
		t.Error("the save root was offered as well — tracking it would put the profile id back in the synced tree")
	}
}
