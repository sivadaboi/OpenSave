package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.0.0", 0},
		{"2.1.0", "2.0.0", 1},
		{"2.0.0", "2.1.0", -1},
		{"2.0.10", "2.0.9", 1}, // numeric, not lexical
		{"2.1", "2.0.5", 1},
		{"2.0", "2.0.0", 0},
		{"10.0.0", "9.9.9", 1},
		{"1.0.0", "2.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSelectUpdateAssetFor(t *testing.T) {
	assets := []releaseAsset{
		{Name: "OpenSave.Setup.exe", BrowserDownloadURL: "u/setup"},
		{Name: "OpenSave.exe", BrowserDownloadURL: "u/portable"},
		{Name: "opensave-cli.exe", BrowserDownloadURL: "u/cli"},
		{Name: "opensave-relay.exe", BrowserDownloadURL: "u/relay"},
		{Name: "opensave-linux-amd64.tar.gz", BrowserDownloadURL: "u/linux"},
	}
	if got := selectUpdateAssetFor(assets, "windows"); got != "u/portable" {
		t.Errorf("windows asset = %q, want u/portable", got)
	}
	if got := selectUpdateAssetFor(assets, "linux"); got != "u/linux" {
		t.Errorf("linux asset = %q, want u/linux", got)
	}
	if got := selectUpdateAssetFor(nil, "linux"); got != "" {
		t.Errorf("no assets should yield empty, got %q", got)
	}
}

func TestExtractAppBinary(t *testing.T) {
	// Build a tarball like the release: opensave-linux/{opensave,cli,relay}.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"opensave-linux/opensave-cli":   "cli-bytes",
		"opensave-linux/opensave":       "APP-BINARY-CONTENT",
		"opensave-linux/opensave-relay": "relay-bytes",
	}
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(content)), Mode: 0o755})
		_, _ = tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()

	archive := filepath.Join(t.TempDir(), "rel.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "opensave.new")
	if err := extractAppBinary(archive, dest); err != nil {
		t.Fatalf("extractAppBinary: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "APP-BINARY-CONTENT" {
		t.Errorf("extracted the wrong file: %q", got)
	}
}
