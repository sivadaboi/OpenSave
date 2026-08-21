package presets

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Measuring is deliberately separate from detection. Scan answers "could a
// save live here", which is a question about layout conventions; this answers
// "did anything ever save here", which is a question about the disk. Keeping
// them apart means the detection tests stay hermetic and fast, and it means a
// slow or unreadable disk degrades the listing rather than the scan.
//
// The numbers exist to separate a live save folder from a dead one. A real
// library has both: Steam creates a userdata folder for every game you own
// whether or not it keeps saves there, repacks leave their save folder behind
// when you move to another one, and a game reinstalled through a different
// launcher writes somewhere new while the old path sits there forever. On the
// machine this was written against, 50 of 235 detected locations held no files
// at all, and 24 of the 30 games detected in more than one place had a gap of
// over two months between the freshest folder and the stalest.
const (
	// statFileCap bounds one location's walk. Real save folders are far
	// smaller; the cap is here for the pathological case, which is a custom
	// scan path pointed at a games drive, where a subfolder is a whole
	// install. Hitting it still establishes the thing that matters — that the
	// folder is not empty.
	statFileCap = 20000
	// statBudget caps the total wall time spent measuring, across every
	// location. A scan that has found 200 folders should not sit there for a
	// minute stat-ing them, and the listing is useful without every number in
	// it.
	statBudget = 8 * time.Second
	// statConcurrency bounds simultaneous walks. Measuring is IO-bound, so
	// some overlap pays for itself on an SSD, but a hundred concurrent walks
	// would thrash a spinning disk.
	statConcurrency = 6
	// statClockCheckEvery is how many files pass between deadline checks.
	// Checking per file would be its own cost on a folder with thousands.
	statClockCheckEvery = 256
)

// errStopWalk aborts a walk that has hit the file cap or the deadline. It
// never escapes measureOne.
var errStopWalk = errors.New("stop")

// Measure fills in each save's file count, size and last-written time,
// in place.
//
// Anything it could not measure — an unreadable folder, or a location it
// never reached before the budget ran out — is left with Measured false, and
// callers must treat that as "unknown" rather than as "empty". Hiding a
// location because measuring it failed would be a way to lose a save, which
// is the one outcome this whole application exists to prevent.
func Measure(saves []DiscoveredSave) {
	if len(saves) == 0 {
		return
	}
	deadline := time.Now().Add(statBudget)

	var wg sync.WaitGroup
	sem := make(chan struct{}, statConcurrency)
	for i := range saves {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Checked before queueing as well as after, so that a scan whose
			// budget is already spent finishes immediately instead of walking
			// the queue one slot at a time.
			if time.Now().After(deadline) {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			if time.Now().After(deadline) {
				return
			}
			measureInto(&saves[i], deadline)
		}(i)
	}
	wg.Wait()
}

// measureInto measures one location and writes the result onto it.
func measureInto(s *DiscoveredSave, deadline time.Time) {
	count, bytes, latest, truncated, ok := measureOne(s.SavePath, deadline)
	if !ok {
		return
	}
	s.Measured = true
	s.FileCount = count
	s.TotalBytes = bytes
	s.LatestMtime = latest
	s.Truncated = truncated
}

// measureOne walks a location, returning ok false when it could not be read
// at all. A partially readable folder returns ok true with numbers that
// understate, which is the right way round: a folder with something in it
// must never look empty.
func measureOne(path string, deadline time.Time) (count int, bytes int64, latest int64, truncated, ok bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, false, false
	}
	// A tracked location can be a single file — several RPG Maker titles are
	// detected that way.
	if !info.IsDir() {
		return 1, info.Size(), info.ModTime().Unix(), false, true
	}

	since := 0
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is skipped rather than fatal. Permission
			// denied on one folder inside a save is not a reason to report
			// nothing about the rest of it.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// A junction is not a directory as far as Go is concerned, so without
		// this it falls through and is counted as a file — inflating the
		// count, and letting a folder holding nothing but a junction look
		// like it holds a save. delta.BuildManifest skips these for the same
		// reason, and the two numbers are read side by side.
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		since++
		if since >= statClockCheckEvery {
			since = 0
			if time.Now().After(deadline) {
				truncated = true
				return errStopWalk
			}
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		count++
		bytes += fi.Size()
		if m := fi.ModTime().Unix(); m > latest {
			latest = m
		}
		if count >= statFileCap {
			truncated = true
			return errStopWalk
		}
		return nil
	})
	// WalkDir does not follow symlinks, so a junction loop in AppData cannot
	// make this run forever.
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		// The root itself became unreadable mid-walk. Anything already counted
		// still stands.
		return count, bytes, latest, truncated, count > 0
	}
	return count, bytes, latest, truncated, true
}

// IsEmpty reports whether a location was measured and found to hold nothing.
//
// Unmeasured locations are not empty by this definition. Callers use it to
// decide what to hide, and hiding the unknown is how a real save goes missing.
func (d DiscoveredSave) IsEmpty() bool {
	return d.Measured && d.FileCount == 0
}

// CountEmpty returns how many of these locations are known to hold nothing.
func CountEmpty(saves []DiscoveredSave) int {
	n := 0
	for _, s := range saves {
		if s.IsEmpty() {
			n++
		}
	}
	return n
}

// WithoutEmpty drops the locations that are known to hold nothing, preserving
// order. Locations that could not be measured are kept.
func WithoutEmpty(saves []DiscoveredSave) []DiscoveredSave {
	out := make([]DiscoveredSave, 0, len(saves))
	for _, s := range saves {
		if !s.IsEmpty() {
			out = append(out, s)
		}
	}
	return out
}
