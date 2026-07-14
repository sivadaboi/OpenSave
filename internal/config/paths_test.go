package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_FreshInstall_CreatesHomeAndBackupsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	paths, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	wantHome := filepath.Join(home, homeDirName)
	if paths.HomeDir != wantHome {
		t.Errorf("HomeDir = %q, want %q", paths.HomeDir, wantHome)
	}
	if _, err := os.Stat(paths.HomeDir); err != nil {
		t.Errorf("home dir not created: %v", err)
	}
	if _, err := os.Stat(paths.BackupsDir); err != nil {
		t.Errorf("backups dir not created: %v", err)
	}
}

func TestResolve_MigratesLegacySavesyncDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	oldHome := filepath.Join(home, oldHomeDirName)
	if err := os.MkdirAll(oldHome, 0o777); err != nil {
		t.Fatal(err)
	}
	oldDBPath := filepath.Join(oldHome, "savesync-db.json")
	if err := os.WriteFile(oldDBPath, []byte(`{"settings":{}}`), 0o666); err != nil {
		t.Fatal(err)
	}

	paths, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if _, err := os.Stat(oldHome); !os.IsNotExist(err) {
		t.Errorf("old home dir %q should no longer exist, stat err = %v", oldHome, err)
	}
	if _, err := os.Stat(paths.LegacyDB); err != nil {
		t.Errorf("expected migrated legacy db at %q: %v", paths.LegacyDB, err)
	}
}

func TestResolve_DoesNotOverwriteExistingNewHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	oldHome := filepath.Join(home, oldHomeDirName)
	newHome := filepath.Join(home, homeDirName)
	if err := os.MkdirAll(oldHome, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newHome, 0o777); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(newHome, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep me"), 0o666); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve(); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("existing new home dir contents should be preserved: %v", err)
	}
	if _, err := os.Stat(oldHome); err != nil {
		t.Errorf("old home dir should be left alone when new home already exists: %v", err)
	}
}
