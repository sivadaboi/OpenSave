package delta

// FileDiff describes how a single file differs between a local and remote
// manifest.
type FileDiff struct {
	RelPath          string
	Added            bool
	Deleted          bool
	ModifiedBlocks   []int // block indices that differ (or are new, past local's block count)
	RemoteBlockCount int
	RemoteBlockSize  int
}

// ManifestDiff is the result of comparing a local manifest against a remote
// one: what's new/changed on the remote side, and what the remote is
// missing (used to decide push direction as well as pull).
type ManifestDiff struct {
	FilesToPull   []FileDiff // exists remotely, missing or different locally
	FilesToPush   []FileDiff // exists locally, missing or different remotely (empty ModifiedBlocks -> use local blocks)
	DirsToCreate  []string
	DirsToDelete  []string
}

// DiffManifests compares local against remote and reports, from the local
// side's perspective, which files need to be pulled from remote and which
// need to be pushed to remote. Mirrors delta.js's diffManifests().
func DiffManifests(local, remote Manifest) ManifestDiff {
	var diff ManifestDiff

	for relPath, remoteEntry := range remote.Files {
		localEntry, exists := local.Files[relPath]
		if !exists {
			diff.FilesToPull = append(diff.FilesToPull, FileDiff{
				RelPath:          relPath,
				Added:            true,
				ModifiedBlocks:   allBlockIndices(len(remoteEntry.Blocks)),
				RemoteBlockCount: len(remoteEntry.Blocks),
				RemoteBlockSize:  remoteEntry.BlockSize,
			})
			continue
		}
		if localEntry.Hash == remoteEntry.Hash {
			continue
		}
		diff.FilesToPull = append(diff.FilesToPull, FileDiff{
			RelPath:          relPath,
			ModifiedBlocks:   diffBlocks(localEntry.Blocks, remoteEntry.Blocks),
			RemoteBlockCount: len(remoteEntry.Blocks),
			RemoteBlockSize:  remoteEntry.BlockSize,
		})
	}

	for relPath, localEntry := range local.Files {
		remoteEntry, exists := remote.Files[relPath]
		if !exists {
			diff.FilesToPush = append(diff.FilesToPush, FileDiff{
				RelPath: relPath,
				Added:   true,
			})
			continue
		}
		if localEntry.Hash == remoteEntry.Hash {
			continue
		}
		diff.FilesToPush = append(diff.FilesToPush, FileDiff{RelPath: relPath})
	}

	remoteDirSet := toSet(remote.Dirs)
	localDirSet := toSet(local.Dirs)
	for _, d := range remote.Dirs {
		if _, ok := localDirSet[d]; !ok {
			diff.DirsToCreate = append(diff.DirsToCreate, d)
		}
	}
	for _, d := range local.Dirs {
		if _, ok := remoteDirSet[d]; !ok {
			diff.DirsToDelete = append(diff.DirsToDelete, d)
		}
	}

	return diff
}

// diffBlocks returns the indices of blocks that differ between local and
// remote, including any index beyond local's block count (file grew) and
// excluding trailing local-only blocks (file shrank — handled by the
// truncate-on-patch step in PatchFile via the remote's own block count).
func diffBlocks(localBlocks, remoteBlocks []Block) []int {
	var changed []int
	for i, remoteBlock := range remoteBlocks {
		if i >= len(localBlocks) || localBlocks[i].Hash != remoteBlock.Hash {
			changed = append(changed, i)
		}
	}
	return changed
}

func allBlockIndices(n int) []int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

func toSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}
