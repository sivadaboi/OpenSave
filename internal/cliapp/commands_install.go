package cliapp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cmdInstall copies this binary somewhere permanent and puts that directory
// on the user's PATH, so `opensave` works from any new terminal.
//
// install.ps1 and install.sh already do this for people who pipe the
// installer to a shell. The release also publishes the bare binary, though,
// and someone who downloads that gets a loose executable with nothing to
// wire it up — they have to know to move it somewhere and edit PATH by hand.
// This closes that gap from the binary itself, so however it was obtained,
// one command finishes the job.
//
// Deliberately a command rather than something the binary does on first run.
// Editing PATH is a change to the user's environment that outlives the
// process; a tool that does that because you merely ran it once is a tool
// that cannot be tried out.
func cmdInstall(args []string) int {
	dir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --dir needs a directory")
				return 1
			}
			dir = args[i+1]
			i++
		case "--help", "-h":
			fmt.Println("Usage: opensave install [--dir <directory>]")
			fmt.Println()
			fmt.Println("Copies this binary to a permanent location and adds it to your PATH,")
			fmt.Println("so `opensave` works from any terminal.")
			fmt.Println()
			fmt.Println("  --dir <directory>   install here instead of the default")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: unknown option %q\n", args[i])
			return 1
		}
	}

	if dir == "" {
		d, err := defaultInstallDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		dir = d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	dir = abs

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot locate this binary: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	dest := filepath.Join(dir, installedName)

	// Running from the destination already (a re-run of `opensave install`,
	// or an installed copy): copying a file onto itself truncates it. Skip
	// the copy and go straight to repairing PATH, which is the part that is
	// actually worth re-running.
	if sameFile(self, dest) {
		fmt.Printf("Already installed at %s\n", dest)
	} else {
		if err := copyExecutable(self, dest); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("Installed %s\n", dest)
	}

	for _, line := range writeAliases(dir) {
		fmt.Printf("  %s\n", line)
	}

	added, err := ensureOnPath(dir)
	if err != nil {
		// The binary is in place; only PATH failed. Say what to do by hand
		// rather than pretending the whole install failed.
		fmt.Fprintf(os.Stderr, "\nwarning: could not update PATH: %v\n", err)
		fmt.Fprintf(os.Stderr, "Add this directory to your PATH manually:\n  %s\n", dir)
		return 1
	}
	fmt.Println()
	if added {
		fmt.Printf("Added %s to your PATH.\n", dir)
		fmt.Println(pathActivationHint)
	} else {
		fmt.Printf("%s was already on your PATH.\n", dir)
	}
	fmt.Println()
	fmt.Println("Then: opensave scan")
	return 0
}

// sameFile reports whether two paths are the same file on disk, so a re-run
// doesn't copy a binary over itself.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// copyExecutable writes src to dst with the executable bit set, moving any
// existing copy aside first: Windows refuses to overwrite a running binary,
// and on Unix writing into a file another process is executing gives it a
// corrupted image mid-run.
func copyExecutable(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		old := dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("%s is in use — close it and try again", filepath.Base(dst))
		}
		// Best effort: on Windows this fails while the old copy is still
		// running, and the next install cleans it up.
		defer func() { _ = os.Remove(old) }()
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// pathEntries splits a PATH value into its entries, dropping empties.
func pathEntries(path string) []string {
	var out []string
	for _, p := range strings.Split(path, string(os.PathListSeparator)) {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// pathContains reports whether dir is already among a PATH value's entries.
// Compares whole entries rather than substrings: a plain `strings.Contains`
// matches "C:\tools\opensave" inside "C:\tools\opensave-old" and then skips
// an update the user needed.
func pathContains(path, dir string) bool {
	target := normalizePathEntry(dir)
	for _, p := range pathEntries(path) {
		if normalizePathEntry(p) == target {
			return true
		}
	}
	return false
}
