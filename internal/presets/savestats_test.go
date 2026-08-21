package presets

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasure_CountsFilesSizeAndNewestMtime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "save1.dat"), "hello")
	writeFile(t, filepath.Join(dir, "nested", "save2.dat"), "world!!")

	old := time.Now().Add(-72 * time.Hour)
	recent := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "save1.dat"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "nested", "save2.dat"), recent, recent); err != nil {
		t.Fatal(err)
	}

	saves := []DiscoveredSave{{ID: "a", SavePath: dir}}
	Measure(saves)

	got := saves[0]
	if !got.Measured {
		t.Fatal("a readable folder should come back measured")
	}
	if got.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2 (subfolders count)", got.FileCount)
	}
	if got.TotalBytes != 12 {
		t.Errorf("TotalBytes = %d, want 12", got.TotalBytes)
	}
	// The newest file is what says whether the folder is still in use, so an
	// old file sitting beside a recent one must not drag the age backwards.
	if delta := got.LatestMtime - recent.Unix(); delta < -2 || delta > 2 {
		t.Errorf("LatestMtime = %d, want the newer file's %d", got.LatestMtime, recent.Unix())
	}
	if got.IsEmpty() {
		t.Error("a folder with files in it is not empty")
	}
}

func TestMeasure_EmptyFolderIsMeasuredAndEmpty(t *testing.T) {
	dir := t.TempDir()
	// A folder tree with directories but no files anywhere: Steam leaves these
	// behind for every owned game, and they are the bulk of what a scan
	// offers that nobody wants.
	if err := os.MkdirAll(filepath.Join(dir, "remote", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}

	saves := []DiscoveredSave{{ID: "a", SavePath: dir}}
	Measure(saves)

	if !saves[0].Measured {
		t.Fatal("an empty folder is still a measured one")
	}
	if saves[0].FileCount != 0 {
		t.Errorf("FileCount = %d, want 0 — directories are not files", saves[0].FileCount)
	}
	if saves[0].LatestMtime != 0 {
		t.Errorf("LatestMtime = %d, want 0 when nothing was ever written", saves[0].LatestMtime)
	}
	if !saves[0].IsEmpty() {
		t.Error("IsEmpty should be true for a folder holding no files")
	}
}

func TestMeasure_SingleFileLocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DefaultTGOfile1.rpgsave")
	writeFile(t, path, "12345")

	saves := []DiscoveredSave{{ID: "a", SavePath: path}}
	Measure(saves)

	if !saves[0].Measured || saves[0].FileCount != 1 || saves[0].TotalBytes != 5 {
		t.Fatalf("a save that is one file should measure as one file: %+v", saves[0])
	}
	if saves[0].IsEmpty() {
		t.Error("a single-file save is not empty")
	}
}

// A location that cannot be read must never be reported as empty, because
// empty is what gets hidden. Hiding a folder we failed to look inside is how
// a real save disappears from the listing.
func TestMeasure_UnreadableLocationStaysUnmeasured(t *testing.T) {
	saves := []DiscoveredSave{{ID: "a", SavePath: filepath.Join(t.TempDir(), "does-not-exist")}}
	Measure(saves)

	if saves[0].Measured {
		t.Error("a path that could not be read must not claim to be measured")
	}
	if saves[0].IsEmpty() {
		t.Error("unmeasured must not count as empty — it is what decides hiding")
	}
	if len(WithoutEmpty(saves)) != 1 {
		t.Error("WithoutEmpty dropped a location it never managed to measure")
	}
	if CountEmpty(saves) != 0 {
		t.Error("an unmeasured location was counted as empty")
	}
}

func TestWithoutEmpty_KeepsOrderAndDropsOnlyEmpties(t *testing.T) {
	full, empty := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(full, "s.dat"), "x")

	saves := []DiscoveredSave{
		{ID: "keep-1", SavePath: full},
		{ID: "drop", SavePath: empty},
		{ID: "keep-2", SavePath: full},
	}
	Measure(saves)

	if n := CountEmpty(saves); n != 1 {
		t.Fatalf("CountEmpty = %d, want 1", n)
	}
	kept := WithoutEmpty(saves)
	if len(kept) != 2 || kept[0].ID != "keep-1" || kept[1].ID != "keep-2" {
		t.Fatalf("WithoutEmpty = %+v, want keep-1 then keep-2 in order", kept)
	}
}

// Detection must not depend on measurement: Scan describes layout, Measure
// describes the disk, and a caller that skips the second still gets a usable
// first. If these ever merged, every scanner test would start walking disks.
func TestScanLeavesStatsUnmeasured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "save.dat"), "data")

	sc := hermeticScanner()
	sc.LocalLowDir = t.TempDir()
	found := sc.Scan([]string{dir})

	if len(found) == 0 {
		t.Fatal("expected the custom scan path's subfolder to be discovered")
	}
	for _, f := range found {
		if f.Measured || f.FileCount != 0 {
			t.Errorf("Scan filled in stats for %q; measurement belongs to Measure", f.ID)
		}
	}
}

// A junction is not a directory as far as Go is concerned, so a naive walk
// counts it as a file. That inflates the count and — worse — lets a folder
// holding nothing but a junction look like it holds a save, which is the one
// thing the empty-hiding rule must get right. delta.BuildManifest skips
// reparse points too, so counting them here would also disagree with what
// actually syncs.
func TestMeasure_SkipsReparsePoints(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows thing; the same code path covers symlinks elsewhere")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	writeFile(t, filepath.Join(target, "save.dat"), "12345")

	link := filepath.Join(dir, "link")
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Skipf("could not create a junction here: %v", err)
	}

	saves := []DiscoveredSave{{ID: "a", SavePath: dir}}
	Measure(saves)

	if saves[0].FileCount != 1 {
		t.Errorf("FileCount = %d, want 1 — the junction was counted as a file", saves[0].FileCount)
	}
	if saves[0].TotalBytes != 5 {
		t.Errorf("TotalBytes = %d, want 5", saves[0].TotalBytes)
	}
}

// A folder whose only content is a junction holds no save, and must be
// hidden like any other empty one.
func TestMeasure_FolderHoldingOnlyAJunctionIsEmpty(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows thing")
	}
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "other.dat"), "data")

	dir := t.TempDir()
	if err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "link"), elsewhere).Run(); err != nil {
		t.Skipf("could not create a junction here: %v", err)
	}

	saves := []DiscoveredSave{{ID: "a", SavePath: dir}}
	Measure(saves)
	if !saves[0].IsEmpty() {
		t.Errorf("a folder holding only a junction reported %d file(s); it holds no save",
			saves[0].FileCount)
	}
}
