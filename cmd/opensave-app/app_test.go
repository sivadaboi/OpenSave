package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRevealTargetDir covers what "Open folder" resolves to before anything
// is launched: a directory reveals itself, a single-file save reveals its
// parent, and unusable paths come back with a message the user can act on
// rather than a silently dead button.
func TestRevealTargetDir(t *testing.T) {
	base := t.TempDir()

	t.Run("directory reveals itself", func(t *testing.T) {
		dir := filepath.Join(base, "SaveFolder")
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		got, problem := revealTargetDir(dir)
		if problem != "" {
			t.Fatalf("unexpected problem: %s", problem)
		}
		if got != filepath.Clean(dir) {
			t.Errorf("got %q, want %q", got, filepath.Clean(dir))
		}
	})

	t.Run("file reveals its parent", func(t *testing.T) {
		dir := filepath.Join(base, "SingleFileGame")
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "slot1.sav")
		if err := os.WriteFile(file, []byte("save"), 0o666); err != nil {
			t.Fatal(err)
		}
		got, problem := revealTargetDir(file)
		if problem != "" {
			t.Fatalf("unexpected problem: %s", problem)
		}
		if got != filepath.Clean(dir) {
			t.Errorf("got %q, want the containing folder %q", got, filepath.Clean(dir))
		}
	})

	t.Run("empty path is reported", func(t *testing.T) {
		if _, problem := revealTargetDir(""); problem == "" {
			t.Error("empty path should report a problem")
		}
		if _, problem := revealTargetDir("   "); problem == "" {
			t.Error("whitespace-only path should report a problem")
		}
	})

	t.Run("missing folder is reported", func(t *testing.T) {
		_, problem := revealTargetDir(filepath.Join(base, "not-here"))
		if problem == "" {
			t.Fatal("missing folder should report a problem")
		}
		if !strings.Contains(strings.ToLower(problem), "no longer exists") {
			t.Errorf("message should say the folder is gone, got %q", problem)
		}
	})
}
