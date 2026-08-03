package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkdirs creates every path given, relative to root.
func mkdirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(r)), 0o777); err != nil {
			t.Fatal(err)
		}
	}
}

func savePathsOf(found []DiscoveredSave) []string {
	out := make([]string, 0, len(found))
	for _, d := range found {
		out = append(out, d.SavePath)
	}
	return out
}

// The reported case: emulators unzipped to a folder on another drive. The
// presets look under %APPDATA%, which on a portable install does not exist —
// so a scan found nothing at all, for exactly the users most likely to have
// a pile of emulators.
func TestPortable_RetroArchInstallOffersSavesNotTheWholeFolder(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "RetroArch")
	// A portable RetroArch: saves and states beside cores and ROMs.
	mkdirs(t, install, "saves", "states", "cores", "system", "downloads")

	found := portableEmulatorSaves(install)
	if len(found) == 0 {
		t.Fatal("a portable RetroArch install was not recognised")
	}

	paths := savePathsOf(found)
	wantSaves := filepath.Join(install, "saves")
	wantStates := filepath.Join(install, "states")
	var haveSaves, haveStates bool
	for _, p := range paths {
		switch p {
		case wantSaves:
			haveSaves = true
		case wantStates:
			haveStates = true
		case install:
			t.Errorf("the whole install folder was offered as a save location: %s", p)
		}
	}
	if !haveSaves || !haveStates {
		t.Errorf("expected both saves and states, got %v", paths)
	}
	// Nothing containing the cores or ROMs may be offered.
	for _, p := range paths {
		if p == filepath.Join(install, "cores") || p == filepath.Join(install, "system") {
			t.Errorf("a non-save folder was offered: %s", p)
		}
	}
}

// The Citra lineage keeps its data in a "user" folder beside the executable
// when portable, so the tail sits one level deeper than RetroArch's.
func TestPortable_AzaharUsesTheUserFolderBesideTheExecutable(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "Azahar")
	mkdirs(t, install, "user/sdmc/Nintendo 3DS", "user/config")

	found := portableEmulatorSaves(install)
	if len(found) != 1 {
		t.Fatalf("expected one save location for a portable Azahar, got %v", savePathsOf(found))
	}
	want := filepath.Join(install, "user", "sdmc", "Nintendo 3DS")
	if found[0].SavePath != want {
		t.Errorf("save path = %q, want %q", found[0].SavePath, want)
	}
}

// A portable yuzu-lineage install must descend to the title folders for the
// same reason the installed one does: the NAND root carries a per-install
// profile id, and syncing that gives the other device a save it attributes to
// a profile its emulator has never seen.
func TestPortable_EdenDescendsToTitlesNotTheNANDRoot(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "eden")
	nand := "user/nand/user/save/0000000000000000/" +
		"1234567890abcdef1234567890abcdef"
	mkdirs(t, install,
		nand+"/0100000000010000",
		nand+"/010028600EBDA000",
	)
	// A title folder with nothing in it is one the emulator created before
	// the game ever saved, and is deliberately not offered — give each one
	// real content so this exercises the descent rather than that rule.
	for _, title := range []string{"0100000000010000", "010028600EBDA000"} {
		f := filepath.Join(install, filepath.FromSlash(nand), title, "00000001.sav")
		if err := os.WriteFile(f, []byte("save"), 0o666); err != nil {
			t.Fatal(err)
		}
	}

	found := portableEmulatorSaves(install)
	if len(found) != 2 {
		t.Fatalf("expected one entry per title, got %v", savePathsOf(found))
	}
	for _, d := range found {
		if filepath.Base(d.SavePath) == "0000000000000000" {
			t.Errorf("offered the NAND root instead of a title: %s", d.SavePath)
		}
		if len(filepath.Base(d.SavePath)) != 16 {
			t.Errorf("save path does not end at a title id: %s", d.SavePath)
		}
	}
}

// Unzipped builds rarely arrive under a bare name.
func TestPortable_RecognisesVersionedAndSuffixedFolderNames(t *testing.T) {
	for _, name := range []string{"RetroArch", "RetroArch-Win64", "retroarch_1.19", "RetroArch 1.20"} {
		root := t.TempDir()
		install := filepath.Join(root, name)
		mkdirs(t, install, "saves")
		if len(portableEmulatorSaves(install)) == 0 {
			t.Errorf("%q was not recognised as a RetroArch install", name)
		}
	}
}

// The name must actually be the emulator's. A folder that merely contains
// something called "saves" is a game, not RetroArch, and claiming otherwise
// would mislabel half a games drive.
func TestPortable_DoesNotClaimUnrelatedFoldersWithASavesDir(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Elden Ring", "My Game", "Edenring", "backups"} {
		install := filepath.Join(root, name)
		mkdirs(t, install, "saves")
		if found := portableEmulatorSaves(install); len(found) > 0 {
			t.Errorf("%q was wrongly claimed as an emulator: %v", name, savePathsOf(found))
		}
	}
}

// The emulator's name alone is not enough either — without the save folder
// there is nothing to sync, and offering the install root is what this
// exists to prevent.
func TestPortable_RequiresTheSaveFolderToExist(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "RetroArch")
	mkdirs(t, install, "cores", "system")
	if found := portableEmulatorSaves(install); len(found) > 0 {
		t.Errorf("an install with no save folder was still offered: %v", savePathsOf(found))
	}
}

// The reported complaint verbatim: "the auto-scan did not find my emulators
// (Azahar, Eden, RetroArch) ... they are all in a folder on a different drive
// than C:". No custom scan path configured — it has to work from a plain scan.
func TestPortable_AutoScanFindsEmulatorsOnAnotherDrive(t *testing.T) {
	drive := t.TempDir() // stands in for D:\
	mkdirs(t, drive,
		"Emulators/RetroArch/saves",
		"Emulators/RetroArch/cores",
		"Emulators/Azahar/user/sdmc/Nintendo 3DS",
		"Emulators/SomethingElse/data", // not an emulator: must be ignored
	)

	prev := driveRoots
	driveRoots = func() []string { return []string{drive} }
	t.Cleanup(func() { driveRoots = prev })

	found := scanFixedDrivesForPortableEmulators()
	paths := savePathsOf(found)

	want := []string{
		filepath.Join(drive, "Emulators", "RetroArch", "saves"),
		filepath.Join(drive, "Emulators", "Azahar", "user", "sdmc", "Nintendo 3DS"),
	}
	for _, w := range want {
		var got bool
		for _, p := range paths {
			if p == w {
				got = true
			}
		}
		if !got {
			t.Errorf("auto-scan did not find %s (found %v)", w, paths)
		}
	}
	for _, p := range paths {
		if p == filepath.Join(drive, "Emulators", "SomethingElse") ||
			p == filepath.Join(drive, "Emulators", "SomethingElse", "data") {
			t.Errorf("a non-emulator folder was claimed: %s", p)
		}
	}
}

// An emulator sitting straight at the top of a drive, with no container.
func TestPortable_AutoScanFindsAnEmulatorAtTheDriveRoot(t *testing.T) {
	drive := t.TempDir()
	mkdirs(t, drive, "RetroArch/saves")

	prev := driveRoots
	driveRoots = func() []string { return []string{drive} }
	t.Cleanup(func() { driveRoots = prev })

	found := scanFixedDrivesForPortableEmulators()
	if len(found) == 0 {
		t.Fatal("an emulator at the drive root was not found")
	}
	if found[0].SavePath != filepath.Join(drive, "RetroArch", "saves") {
		t.Errorf("save path = %q", found[0].SavePath)
	}
}

// The sweep must stay cheap: one listing per drive, one more per folder that
// is actually named like an emulator collection, and no walking. A games
// drive holds tens of thousands of directories and a scan that descends into
// them would take minutes to find nothing.
func TestPortable_AutoScanDoesNotWalkTheWholeDrive(t *testing.T) {
	drive := t.TempDir()
	// A deep tree that must never be descended into.
	mkdirs(t, drive, "SteamLibrary/steamapps/common/Some Game/Engine/Binaries/Win64/saves")
	// And a decoy at a depth the sweep is not allowed to reach.
	mkdirs(t, drive, "Downloads/Stuff/RetroArch/saves")

	prev := driveRoots
	driveRoots = func() []string { return []string{drive} }
	t.Cleanup(func() { driveRoots = prev })

	if found := scanFixedDrivesForPortableEmulators(); len(found) > 0 {
		t.Errorf("the sweep reached deeper than it should: %v", savePathsOf(found))
	}
}

// The property the whole feature depends on: a portable install must be
// offered under exactly the name an installed copy gets.
//
// Two devices resolve each other's games by id, and tracking derives that id
// by slugifying the name. So a portable RetroArch on one machine and an
// installed one on the other only ever sync if both produce the same name —
// label one "(portable)" and they become two different games that never
// meet, which is precisely the saves this feature exists to move.
func TestPortable_NameMatchesTheInstalledEquivalent(t *testing.T) {
	// What each preset is called when found the ordinary way.
	installedName := map[string]string{}
	for _, p := range presetDefs {
		installedName[p.ID] = p.Name
	}

	root := t.TempDir()
	cases := []struct {
		dir, layout, presetID string
	}{
		{"RetroArch", "saves", "retroarch-saves"},
		{"RetroArch", "states", "retroarch-states"},
		{"Azahar", "user/sdmc/Nintendo 3DS", "azahar"},
	}
	for _, c := range cases {
		install := filepath.Join(root, c.dir)
		mkdirs(t, install, c.layout)
	}

	for _, c := range cases {
		found := portableEmulatorSaves(filepath.Join(root, c.dir))
		var matched bool
		for _, d := range found {
			if d.Name == installedName[c.presetID] {
				matched = true
			}
			if strings.Contains(d.Name, "portable") {
				t.Errorf("%q carries a name an installed copy never has, so the two "+
					"devices would derive different game ids: %q", c.dir, d.Name)
			}
		}
		if !matched {
			var names []string
			for _, d := range found {
				names = append(names, d.Name)
			}
			t.Errorf("no entry named %q for a portable %s (got %v)",
				installedName[c.presetID], c.dir, names)
		}
	}
}

// A custom scan path pointing at the folder that HOLDS the emulators must
// narrow to their saves rather than offering each install whole.
func TestPortable_CustomScanPathNarrowsToSaves(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "RetroArch/saves", "RetroArch/cores", "Azahar/user/sdmc/Nintendo 3DS")
	// A plain game folder alongside them still behaves as before.
	mkdirs(t, root, "Some Game")

	sc := NewScanner(filepath.Join(t.TempDir(), "cache.json"))
	found := sc.Scan([]string{root})

	var sawRetroSaves, sawAzahar, sawWholeInstall, sawPlainGame bool
	for _, d := range found {
		switch d.SavePath {
		case filepath.Join(root, "RetroArch", "saves"):
			sawRetroSaves = true
		case filepath.Join(root, "Azahar", "user", "sdmc", "Nintendo 3DS"):
			sawAzahar = true
		case filepath.Join(root, "RetroArch"), filepath.Join(root, "Azahar"):
			sawWholeInstall = true
		case filepath.Join(root, "Some Game"):
			sawPlainGame = true
		}
	}
	if !sawRetroSaves {
		t.Error("RetroArch's saves folder was not offered")
	}
	if !sawAzahar {
		t.Error("Azahar's save folder was not offered")
	}
	if sawWholeInstall {
		t.Error("an emulator install folder was offered whole")
	}
	if !sawPlainGame {
		t.Error("an ordinary folder alongside the emulators stopped being offered")
	}
}
