// Package delta implements block-level content hashing and manifest
// diff/patch, mirroring src/daemon/delta.js from the original Node app:
// variable block size (64KB / 512KB above 20MB / 2MB above 100MB), SHA-256
// per block plus a whole-file hash, and a manifest tree of relative paths.
package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TmpSuffix marks the temp files PatchFile writes before atomically
// replacing the target. They are never part of the save itself: manifests
// must exclude them, or an interrupted patch's leftover would sync to the
// peer as a real file (and cascade into name.opensave.tmp.opensave.tmp).
const TmpSuffix = ".opensave.tmp"

// staleTmpAge is how old a leftover TmpSuffix file must be before the
// manifest walk garbage-collects it. Generous enough that a patch actively
// writing its temp file is never deleted out from under it.
const staleTmpAge = 15 * time.Minute

const (
	defaultBlockSize = 64 * 1024
	mediumBlockSize  = 512 * 1024
	largeBlockSize   = 2 * 1024 * 1024

	mediumFileThreshold = 20 * 1024 * 1024
	largeFileThreshold  = 100 * 1024 * 1024
)

// BlockSizeFor returns the chunking block size for a file of the given
// size, matching delta.js's scaling thresholds exactly.
func BlockSizeFor(fileSize int64) int {
	switch {
	case fileSize > largeFileThreshold:
		return largeBlockSize
	case fileSize > mediumFileThreshold:
		return mediumBlockSize
	default:
		return defaultBlockSize
	}
}

// Block describes one fixed-size (except possibly the last) chunk of a file.
type Block struct {
	Index  int    `json:"index"`
	Hash   string `json:"hash"`
	Length int    `json:"length"`
}

// Milli is a millisecond timestamp. It unmarshals from any JSON number —
// the original JS daemon sends Node's fractional mtimeMs values
// (e.g. 1783279365872.0251), which would fail to decode into a plain
// int64 — and marshals back as a whole integer.
type Milli int64

// UnmarshalJSON accepts integers and floats, truncating fractions.
func (m *Milli) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*m = Milli(f)
	return nil
}

// MarshalJSON emits a plain integer.
func (m Milli) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(m), 10)), nil
}

// FileEntry is one file's record inside a Manifest.
type FileEntry struct {
	Size      int64   `json:"size"`
	Hash      string  `json:"hash"`
	Blocks    []Block `json:"blocks"`
	BlockSize int     `json:"blockSize"`
	MtimeMs   Milli   `json:"mtime"`
}

// Manifest is the full tree snapshot of a tracked save folder (or single
// file), keyed by path relative to the save root.
type Manifest struct {
	Timestamp   time.Time            `json:"timestamp"`
	LatestMtime Milli                `json:"latestMtime"`
	Files       map[string]FileEntry `json:"files"`
	Dirs        []string             `json:"dirs"`

	// Extra carries save locations beyond the primary one, keyed by root
	// name. It is omitted entirely for single-root games, which is nearly
	// all of them.
	//
	// Files and Dirs above always describe the PRIMARY location and nothing
	// else — deliberately, and it is the property that makes multi-root safe
	// to put on the wire. A peer that predates this field decodes the same
	// manifest it always did and syncs the primary folder correctly; it does
	// not see the extra roots, and crucially it cannot mistake them for
	// subfolders of the primary one and write a config file into a save
	// directory. Degrading to a partial sync is recoverable. Scattering
	// files into the wrong folders is not.
	Extra map[string]RootManifest `json:"extraRoots,omitempty"`
}

// RootManifest is one save location's contents, in the same shape the
// primary location uses.
type RootManifest struct {
	Files map[string]FileEntry `json:"files"`
	Dirs  []string             `json:"dirs"`
}

// PrimaryRoot is the reserved name of a game's main save location — the one
// held in Game.SavePath rather than in the roots table.
const PrimaryRoot = ""

// RootNames lists every location in this manifest, primary first and the
// rest sorted, so callers iterate in a stable order.
func (m Manifest) RootNames() []string {
	names := make([]string, 0, len(m.Extra)+1)
	names = append(names, PrimaryRoot)
	for n := range m.Extra {
		names = append(names, n)
	}
	sort.Strings(names[1:])
	return names
}

// Root returns one location's contents. An unknown name yields an empty
// RootManifest rather than an error: a peer naming a root this device has
// never heard of is an ordinary state, not a failure.
func (m Manifest) Root(name string) RootManifest {
	if name == PrimaryRoot {
		return RootManifest{Files: m.Files, Dirs: m.Dirs}
	}
	return m.Extra[name]
}

// hashBytes returns the lowercase hex SHA-256 of b.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashFile computes the whole-file SHA-256 and per-block SHA-256 list for
// the file at path, using the block size dictated by its size.
func HashFile(path string) (FileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileEntry{}, err
	}

	blockSize := BlockSizeFor(info.Size())
	f, err := os.Open(path)
	if err != nil {
		return FileEntry{}, err
	}
	defer f.Close()

	whole := sha256.New()
	buf := make([]byte, blockSize)
	var blocks []Block
	index := 0
	for {
		n, readErr := io.ReadFull(f, buf)
		if n > 0 {
			chunk := buf[:n]
			whole.Write(chunk)
			blocks = append(blocks, Block{
				Index:  index,
				Hash:   hashBytes(chunk),
				Length: n,
			})
			index++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return FileEntry{}, readErr
		}
	}

	return FileEntry{
		Size:      info.Size(),
		Hash:      hex.EncodeToString(whole.Sum(nil)),
		Blocks:    blocks,
		BlockSize: blockSize,
		MtimeMs:   Milli(info.ModTime().UnixMilli()),
	}, nil
}

// BuildManifest walks root (a directory or a single file, per
// ResolveLocalSaveFilePath) and returns a full Manifest of its contents.
func BuildManifest(root string) (Manifest, error) {
	// Never scan profile/system-level folders. A mis-tracked game pointing
	// at e.g. C:\Users\<name> would otherwise try to hash (and sync!) the
	// whole user profile — and die on the first legacy junction anyway.
	if reason := DangerousSyncRoot(root); reason != "" {
		return Manifest{}, fmt.Errorf("refusing to scan %q: %s — edit this game's save path so it points at the actual save folder", root, reason)
	}

	info, err := os.Stat(root)
	if err != nil {
		return Manifest{}, err
	}

	m := Manifest{
		Timestamp: time.Now().UTC(),
		Files:     map[string]FileEntry{},
	}

	if !info.IsDir() {
		entry, err := HashFile(root)
		if err != nil {
			return Manifest{}, err
		}
		m.Files[filepath.Base(root)] = entry
		m.LatestMtime = entry.MtimeMs
		return m, nil
	}

	var dirs []string
	err = filepath.Walk(root, func(path string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// Permission-denied subtrees (Windows legacy junctions like
			// AppData\Local\Application Data, locked system dirs) are
			// skipped instead of failing the whole manifest — one
			// unreadable directory must not abort every sync of the game.
			if os.IsPermission(walkErr) {
				if walkInfo != nil && walkInfo.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if path == root {
			return nil
		}
		// Never follow or record symlinks / reparse points: junctions can
		// recurse (Application Data -> its own parent) or point outside the
		// save root entirely.
		if walkInfo.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isDotEntry(rel) {
			if walkInfo.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if walkInfo.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}

		// Leftover atomic-replace temp from an interrupted patch: never a
		// real save file. Exclude it, and garbage-collect it once it's
		// clearly abandoned (old enough that no live patch still owns it).
		if strings.HasSuffix(rel, TmpSuffix) {
			if time.Since(walkInfo.ModTime()) > staleTmpAge {
				_ = os.Remove(path)
			}
			return nil
		}

		entry, err := HashFile(path)
		if err != nil {
			return err
		}
		m.Files[rel] = entry
		if entry.MtimeMs > m.LatestMtime {
			m.LatestMtime = entry.MtimeMs
		}
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}

	sort.Strings(dirs)
	m.Dirs = dirs
	return m, nil
}

// ManifestHash returns a single stable hash summarizing a manifest's
// content: the per-file hashes (sorted by relative path) plus the dir
// list. Two manifests with identical file contents yield the same hash
// regardless of mtimes — this is what the watcher and sync engine compare
// to decide "has anything actually changed".
//
// It covers the PRIMARY location only, and must keep doing so. This value
// is the merge base two devices record as their last agreed state, and a
// merge base is only meaningful if both sides compute it over the same
// thing. Fold extra roots in here and a device that has them would never
// agree with one that does not — not because the saves differ, but because
// the two are measuring different amounts of the game. A base that can
// never be reached is a base that is always behind both sides, which reads
// as permanent two-way divergence: a conflict on every sync, on saves
// nobody touched. Each root carries its own base instead; see RootHash.
//
// For "has anything on disk changed", which does need to span every
// location, use ContentHash.
func (m Manifest) ManifestHash() string { return m.RootHash(PrimaryRoot) }

// RootHash is ManifestHash for one location. Root names are not mixed into
// the digest, so a root's hash depends only on its contents — the same files
// under a different name still compare equal, which matters when a device
// renames a location.
func (m Manifest) RootHash(name string) string {
	r := m.Root(name)
	paths := make([]string, 0, len(r.Files))
	for p := range r.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		io.WriteString(h, p)
		io.WriteString(h, ":")
		io.WriteString(h, r.Files[p].Hash)
		io.WriteString(h, "\n")
	}
	for _, d := range r.Dirs {
		io.WriteString(h, "dir:"+d+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ContentHash summarizes every location, for deciding whether anything on
// this device changed at all — the watcher's "is a snapshot warranted"
// question. It is never exchanged with a peer or recorded as a merge base;
// see ManifestHash for why that distinction is load-bearing.
//
// For a single-root game it is deliberately identical to ManifestHash, so
// the value the watcher already stored against existing games stays valid
// across the upgrade and nobody gets a spurious snapshot on first run.
func (m Manifest) ContentHash() string {
	if len(m.Extra) == 0 {
		return m.ManifestHash()
	}
	h := sha256.New()
	for _, name := range m.RootNames() {
		io.WriteString(h, name)
		io.WriteString(h, "=")
		io.WriteString(h, m.RootHash(name))
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BuildMultiManifest builds a manifest spanning a game's primary location
// and any extra ones, keyed by root name.
//
// extra maps root name to this device's path for it, and only mapped roots
// belong in it — a root this device has no path for is simply absent from
// the manifest, which is exactly how it should read to a peer: "I do not
// have that here", not "that is empty here". The difference matters, since
// the second would propagate as a deletion.
//
// A location that cannot be read (drive unplugged, folder deleted since it
// was configured) is skipped with its error returned alongside the manifest.
// One missing extra folder must not stop the primary save from syncing —
// but it also must not be silently reported as empty, for the same reason.
func BuildMultiManifest(primary string, extra map[string]string) (Manifest, map[string]error, error) {
	m, err := BuildManifest(primary)
	if err != nil {
		return Manifest{}, nil, err
	}
	if len(extra) == 0 {
		return m, nil, nil
	}

	var failures map[string]error
	for name, path := range extra {
		if name == PrimaryRoot || strings.TrimSpace(path) == "" {
			continue // guarded in the store too; belt and braces
		}
		sub, err := BuildManifest(path)
		if err != nil {
			if failures == nil {
				failures = map[string]error{}
			}
			failures[name] = err
			continue
		}
		if m.Extra == nil {
			m.Extra = map[string]RootManifest{}
		}
		m.Extra[name] = RootManifest{Files: sub.Files, Dirs: sub.Dirs}
		if sub.LatestMtime > m.LatestMtime {
			m.LatestMtime = sub.LatestMtime
		}
	}
	return m, failures, nil
}

// isDotEntry reports whether any path segment of rel starts with a dot,
// matching chokidar's default `ignored: /(^|[\/\\])\../` behavior.
func isDotEntry(rel string) bool {
	for _, seg := range splitSlash(rel) {
		if len(seg) > 0 && seg[0] == '.' {
			return true
		}
	}
	return false
}

func splitSlash(p string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			parts = append(parts, p[start:i])
			start = i + 1
		}
	}
	parts = append(parts, p[start:])
	return parts
}
