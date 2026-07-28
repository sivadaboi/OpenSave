//go:build windows

package cliapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The PATH value this produces gets written to HKCU\Environment, so a
// mistake here does not fail loudly — it silently rewrites the user's PATH
// and breaks unrelated tools. Every case below is one way that has happened
// to real installers.
func TestNextPathValue(t *testing.T) {
	const dir = `C:\Users\x\AppData\Local\OpenSave\bin`

	for _, tc := range []struct {
		name    string
		current string
		want    string
	}{
		{
			name:    "empty PATH takes the directory alone",
			current: "",
			want:    dir,
		},
		{
			name:    "appends to an existing PATH",
			current: `C:\Windows;C:\Windows\System32`,
			want:    `C:\Windows;C:\Windows\System32;` + dir,
		},
		{
			// Windows paths are case-insensitive; a re-run under a different
			// spelling must not append a second copy.
			name:    "already present in another case is left alone",
			current: `C:\Windows;c:\users\x\appdata\local\opensave\BIN`,
			want:    "",
		},
		{
			name:    "already present with a trailing slash is left alone",
			current: dir + `\`,
			want:    "",
		},
		{
			// The bug a substring check would introduce: this entry merely
			// starts with the same text, so the real directory is missing
			// and must still be added.
			name:    "a longer similarly-named entry does not count as present",
			current: dir + `-old`,
			want:    dir + `-old;` + dir,
		},
		{
			name:    "a trailing semicolon does not produce an empty entry",
			current: `C:\Windows;`,
			want:    `C:\Windows;` + dir,
		},
		{
			// %VAR% references must survive verbatim: expanding them is what
			// turns "add one entry" into "rewrite the whole PATH".
			name:    "unexpanded variable references are preserved",
			current: `%USERPROFILE%\bin;C:\Windows`,
			want:    `%USERPROFILE%\bin;C:\Windows;` + dir,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextPathValue(tc.current, dir); got != tc.want {
				t.Errorf("nextPathValue(%q)\n got: %q\nwant: %q", tc.current, got, tc.want)
			}
		})
	}
}

// The alias shims are what make `os` work, and a malformed .cmd fails in a
// way that is hard to read at a prompt.
func TestWriteAliasesProducesForwardingShims(t *testing.T) {
	dir := t.TempDir()

	written := writeAliases(dir)
	if len(written) != 2 {
		t.Fatalf("expected shims for os and opensave-cli, got %v", written)
	}

	for _, alias := range []string{"os", "opensave-cli"} {
		body, err := os.ReadFile(filepath.Join(dir, alias+".cmd"))
		if err != nil {
			t.Fatalf("reading %s shim: %v", alias, err)
		}
		got := string(body)
		if !strings.Contains(got, installedName) {
			t.Errorf("%s shim does not call %s: %q", alias, installedName, got)
		}
		// %~dp0 keeps the shim working wherever the directory is moved, and
		// %* is what forwards the user's arguments.
		if !strings.Contains(got, "%~dp0") {
			t.Errorf("%s shim uses an absolute path instead of %%~dp0: %q", alias, got)
		}
		if !strings.Contains(got, "%*") {
			t.Errorf("%s shim drops arguments: %q", alias, got)
		}
	}
}

// Installing over a previous copy has to work — it is the update path.
func TestCopyExecutableReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.exe")
	dst := filepath.Join(dir, "dst.exe")

	if err := os.WriteFile(src, []byte("new-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old-build"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyExecutable(src, dst); err != nil {
		t.Fatalf("copyExecutable error = %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-build" {
		t.Errorf("destination = %q, want the new build", got)
	}
}
