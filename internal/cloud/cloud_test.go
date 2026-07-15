package cloud

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	svc := New(s, func(level, msg string) {})
	return svc, s
}

func setCloudConfig(t *testing.T, s *store.Store, mutate func(*store.CloudConfig)) {
	t.Helper()
	cfg, err := s.GetCloudConfig()
	if err != nil {
		t.Fatal(err)
	}
	mutate(&cfg)
	if err := s.UpdateCloudConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func writeTempZip(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap.zip")
	if err := os.WriteFile(p, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocalFolderRoundTrip(t *testing.T) {
	svc, s := newTestService(t)
	destDir := t.TempDir()
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "local"
		c.URL = destDir
	})

	src := writeTempZip(t, "zip bytes")
	if err := svc.Upload(src, "game__main__snap_1.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	files, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_1.zip" {
		t.Fatalf("List = %+v", files)
	}

	dl := filepath.Join(t.TempDir(), "restored.zip")
	if err := svc.Download("game__main__snap_1.zip", dl); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dl)
	if string(got) != "zip bytes" {
		t.Errorf("downloaded = %q", got)
	}
}

func TestWebDAVProvider(t *testing.T) {
	var uploaded []byte
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodPut:
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case "PROPFIND":
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprint(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/</D:href>
    <D:propstat><D:prop><D:getcontentlength>0</D:getcontentlength></D:prop></D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/game__main__snap_9.zip</D:href>
    <D:propstat><D:prop>
      <D:getcontentlength>1234</D:getcontentlength>
      <D:getlastmodified>Wed, 01 Jul 2026 10:00:00 GMT</D:getlastmodified>
    </D:prop></D:propstat>
  </D:response>
</D:multistatus>`)
		case http.MethodGet:
			fmt.Fprint(w, "webdav content")
		}
	}))
	defer server.Close()

	svc, s := newTestService(t)
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "webdav"
		c.URL = server.URL + "/dav"
		c.Username = "user"
		c.Password = "pass"
	})

	if err := svc.Upload(writeTempZip(t, "dav data"), "game__main__snap_9.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if string(uploaded) != "dav data" {
		t.Errorf("server received %q", uploaded)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != wantAuth {
		t.Errorf("auth header = %q, want %q", gotAuth, wantAuth)
	}

	files, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_9.zip" || files[0].SizeBytes != 1234 {
		t.Errorf("List = %+v", files)
	}

	dl := filepath.Join(t.TempDir(), "out.zip")
	if err := svc.Download("game__main__snap_9.zip", dl); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dl)
	if string(got) != "webdav content" {
		t.Errorf("downloaded = %q", got)
	}
}

func TestGoogleDriveProviderWithTokenRefresh(t *testing.T) {
	refreshCalls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Relay OAuth proxy: refresh grant.
		refreshCalls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-old" {
			t.Errorf("unexpected proxy payload: %v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-fresh", "expires_in": 3600})
	}))
	defer proxy.Close()

	var driveURL string
	uploaded := &bytes.Buffer{}
	drive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Chunk PUTs go to the pre-authorized session URL (no auth header
		// required, mirroring the real API).
		if r.URL.Path == "/resumable-session" {
			_, _ = io.Copy(uploaded, r.Body)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"id":"file123"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer at-fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/upload/"):
			// Resumable initiation: hand back the session URL.
			w.Header().Set("Location", driveURL+"/resumable-session")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/drive/v3/files":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]any{
					{"id": "f1", "name": "game__main__snap_5.zip", "size": "2048", "createdTime": "2026-07-01T00:00:00Z"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/drive/v3/files/f1"):
			fmt.Fprint(w, "drive bytes")
		}
	}))
	defer drive.Close()
	driveURL = drive.URL

	svc, s := newTestService(t)
	// Point the relay (and thus the OAuth proxy) at the mock.
	settings, _ := s.GetSettings()
	settings.RelayURL = strings.Replace(proxy.URL, "http://", "ws://", 1)
	if err := s.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "google_drive"
		c.AccessToken = "at-expired"
		c.RefreshToken = "rt-old"
		c.ExpiryTimeMs = time.Now().UnixMilli() - 1000 // already expired -> must refresh
	})
	svc.Endpoints.GoogleAPI = drive.URL
	svc.Endpoints.GoogleUpload = drive.URL

	if err := svc.Upload(writeTempZip(t, "drive data"), "game__main__snap_5.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh calls = %d, want 1", refreshCalls)
	}

	// The refreshed token must be persisted (no second refresh).
	files, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].SizeBytes != 2048 {
		t.Errorf("List = %+v", files)
	}
	if refreshCalls != 1 {
		t.Errorf("token should be reused after refresh, refresh calls = %d", refreshCalls)
	}

	dl := filepath.Join(t.TempDir(), "out.zip")
	if err := svc.Download("game__main__snap_5.zip", dl); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dl)
	if string(got) != "drive bytes" {
		t.Errorf("downloaded = %q", got)
	}
}

// TestPruneGameBranch verifies cloud retention: newest `keep` snapshots
// stay, older ones are deleted, other games/branches are untouched.
func TestPruneGameBranch(t *testing.T) {
	svc, s := newTestService(t)
	destDir := t.TempDir()
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "local"
		c.URL = destDir
	})

	// Upload 5 snapshots for game__main plus one for another branch/game.
	names := []string{
		"game__main__snap_1.zip", "game__main__snap_2.zip", "game__main__snap_3.zip",
		"game__main__snap_4.zip", "game__main__snap_5.zip",
		"game__ngplus__snap_9.zip", "other__main__snap_1.zip",
	}
	for i, n := range names {
		if err := svc.Upload(writeTempZip(t, "data"), n); err != nil {
			t.Fatal(err)
		}
		// Stagger mtimes so CreatedTime ordering is deterministic.
		older := time.Now().Add(-time.Duration(len(names)-i) * time.Minute)
		_ = os.Chtimes(filepath.Join(destDir, n), older, older)
	}

	pruned, err := svc.PruneGameBranch(func(name string) bool {
		return strings.HasPrefix(name, "game__main__")
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}

	remaining, _ := svc.List()
	got := map[string]bool{}
	for _, f := range remaining {
		got[f.Name] = true
	}
	for _, want := range []string{"game__main__snap_3.zip", "game__main__snap_4.zip", "game__main__snap_5.zip", "game__ngplus__snap_9.zip", "other__main__snap_1.zip"} {
		if !got[want] {
			t.Errorf("%s should have been kept", want)
		}
	}
	for _, gone := range []string{"game__main__snap_1.zip", "game__main__snap_2.zip"} {
		if got[gone] {
			t.Errorf("%s should have been pruned", gone)
		}
	}

	// keep <= 0 disables pruning entirely.
	if n, _ := svc.PruneGameBranch(func(string) bool { return true }, 0); n != 0 {
		t.Errorf("keep=0 pruned %d files; retention should be disabled", n)
	}
}

// TestRefreshInvalidGrantClearsTokens verifies that a permanently-dead
// refresh token (Google's invalid_grant) wipes the stored credentials so
// the UI stops showing "connected" and the user is told to reconnect.
func TestRefreshInvalidGrantClearsTokens(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	}))
	defer proxy.Close()

	svc, s := newTestService(t)
	settings, _ := s.GetSettings()
	settings.RelayURL = strings.Replace(proxy.URL, "http://", "ws://", 1)
	if err := s.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "google_drive"
		c.AccessToken = "at-expired"
		c.RefreshToken = "rt-dead"
		c.ExpiryTimeMs = time.Now().UnixMilli() - 1000 // forces a refresh
		c.UserEmail = "player@example.com"
	})

	err := svc.Upload(writeTempZip(t, "data"), "game__main__snap_1.zip")
	if err == nil {
		t.Fatal("expected upload to fail on dead refresh token")
	}
	if !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), "reconnect") {
		t.Errorf("error should tell the user to reconnect, got: %v", err)
	}

	// Dead credentials must be wiped so the UI shows "disconnected".
	cfg, _ := s.GetCloudConfig()
	if cfg.AccessToken != "" || cfg.RefreshToken != "" || cfg.UserEmail != "" || cfg.ExpiryTimeMs != 0 {
		t.Errorf("dead tokens should be cleared, got %+v", cfg)
	}
}

func TestDropboxProvider(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2/files/list_folder" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entries": []map[string]any{
					{".tag": "file", "name": "game__main__snap_7.zip", "size": 512, "client_modified": "2026-07-01T00:00:00Z"},
					{".tag": "folder", "name": "subfolder"},
				},
			})
		}
	}))
	defer api.Close()

	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/files/upload":
			var args map[string]any
			_ = json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &args)
			if args["path"] != "/OpenSave/game__main__snap_7.zip" {
				t.Errorf("upload path = %v", args["path"])
			}
			fmt.Fprint(w, `{}`)
		case "/2/files/download":
			fmt.Fprint(w, "dropbox bytes")
		}
	}))
	defer content.Close()

	svc, s := newTestService(t)
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "dropbox"
		c.AccessToken = "at-db"
		c.ExpiryTimeMs = time.Now().UnixMilli() + 3600_000
	})
	svc.Endpoints.DropboxAPI = api.URL
	svc.Endpoints.DropboxContent = content.URL

	if err := svc.Upload(writeTempZip(t, "db data"), "game__main__snap_7.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	files, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_7.zip" {
		t.Errorf("List = %+v (folders must be filtered)", files)
	}
	dl := filepath.Join(t.TempDir(), "out.zip")
	if err := svc.Download("game__main__snap_7.zip", dl); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dl)
	if string(got) != "dropbox bytes" {
		t.Errorf("downloaded = %q", got)
	}
}

func TestOneDriveProvider(t *testing.T) {
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/children"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"name": "game__main__snap_3.zip", "size": 999, "createdDateTime": "2026-07-01T00:00:00Z", "file": map[string]any{}},
					{"name": "notazip.txt", "size": 1, "file": map[string]any{}},
				},
			})
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodGet:
			fmt.Fprint(w, "onedrive bytes")
		}
	}))
	defer graph.Close()

	svc, s := newTestService(t)
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "onedrive"
		c.AccessToken = "at-od"
		c.ExpiryTimeMs = time.Now().UnixMilli() + 3600_000
	})
	svc.Endpoints.Graph = graph.URL

	if err := svc.Upload(writeTempZip(t, "od data"), "game__main__snap_3.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	files, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_3.zip" {
		t.Errorf("List = %+v (non-zips must be filtered)", files)
	}
	dl := filepath.Join(t.TempDir(), "out.zip")
	if err := svc.Download("game__main__snap_3.zip", dl); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dl)
	if string(got) != "onedrive bytes" {
		t.Errorf("downloaded = %q", got)
	}
}

func TestWebhookUpload(t *testing.T) {
	var gotFileName string
	var gotHeader string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("expected multipart form: %v", err)
			return
		}
		_, header, err := r.FormFile("file")
		if err == nil {
			gotFileName = header.Filename
		}
	}))
	defer hook.Close()

	svc, s := newTestService(t)
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "webhook"
		c.URL = hook.URL
		c.HeadersJSON = `{"X-Custom":"secret-token"}`
	})

	if err := svc.Upload(writeTempZip(t, "hook data"), "game__main__snap_2.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotFileName != "game__main__snap_2.zip" {
		t.Errorf("multipart filename = %q", gotFileName)
	}
	if gotHeader != "secret-token" {
		t.Errorf("custom header = %q", gotHeader)
	}
}

func TestPKCEAndAuthURL(t *testing.T) {
	svc, _ := newTestService(t)

	verifier, challenge := GeneratePKCE()
	if verifier == "" || challenge == "" || verifier == challenge {
		t.Fatal("bad PKCE pair")
	}
	if strings.ContainsAny(verifier+challenge, "+/=") {
		t.Error("PKCE values must be base64url without padding")
	}

	u, err := svc.AuthURL("dropbox", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "dropbox.com/oauth2/authorize") ||
		!strings.Contains(u, "code_challenge="+challenge) ||
		!strings.Contains(u, "code_challenge_method=S256") {
		t.Errorf("dropbox auth URL wrong: %s", u)
	}

	if _, err := svc.AuthURL("onedrive", challenge); err == nil {
		t.Error("onedrive without a custom client ID must error (no built-in registration)")
	}

	u, err = svc.AuthURL("google_drive", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "accounts.google.com") || !strings.Contains(u, "access_type=offline") {
		t.Errorf("google auth URL wrong: %s", u)
	}
}

func TestExchangeAuthCodePersistsTokens(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "the-code" {
			t.Errorf("unexpected token form: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "refresh_token": "rt-new", "expires_in": 3600,
		})
	}))
	defer token.Close()

	profile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"email": "user@example.com"})
	}))
	defer profile.Close()

	svc, s := newTestService(t)
	setCloudConfig(t, s, func(c *store.CloudConfig) {
		c.Provider = "dropbox"
	})
	svc.Endpoints.DropboxToken = token.URL
	svc.Endpoints.DropboxAPI = profile.URL

	if err := svc.ExchangeAuthCode("dropbox", "the-code", "the-verifier"); err != nil {
		t.Fatalf("ExchangeAuthCode: %v", err)
	}

	cfg, err := s.GetCloudConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessToken != "at-new" || cfg.RefreshToken != "rt-new" || cfg.UserEmail != "user@example.com" {
		t.Errorf("persisted config wrong: %+v", cfg)
	}
	if cfg.ExpiryTimeMs <= time.Now().UnixMilli() {
		t.Error("expiry must be in the future")
	}

	// Disconnect wipes tokens.
	if err := svc.Disconnect(); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetCloudConfig()
	if cfg.AccessToken != "" || cfg.RefreshToken != "" || cfg.UserEmail != "" {
		t.Errorf("disconnect must wipe tokens: %+v", cfg)
	}
}

// TestChunkedUploads shrinks the thresholds so a small file exercises the
// multi-chunk session protocols end to end.
func TestChunkedUploads(t *testing.T) {
	// 100 KB of data, 32 KB chunks -> 4 chunks.
	payload := bytes.Repeat([]byte("chunky-data-0123"), 6400)
	src := filepath.Join(t.TempDir(), "big.zip")
	if err := os.WriteFile(src, payload, 0o666); err != nil {
		t.Fatal(err)
	}

	t.Run("google drive resumable multi-chunk", func(t *testing.T) {
		old := driveChunkSize
		driveChunkSize = 32 << 10
		defer func() { driveChunkSize = old }()

		var driveURL string
		var got bytes.Buffer
		var chunkRanges []string
		drive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/session" {
				chunkRanges = append(chunkRanges, r.Header.Get("Content-Range"))
				_, _ = io.Copy(&got, r.Body)
				if strings.HasSuffix(r.Header.Get("Content-Range"), fmt.Sprintf("/%d", len(payload))) &&
					len(got.Bytes()) == len(payload) {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(308)
				}
				return
			}
			w.Header().Set("Location", driveURL+"/session")
			w.WriteHeader(http.StatusOK)
		}))
		defer drive.Close()
		driveURL = drive.URL

		svc, s := newTestService(t)
		setCloudConfig(t, s, func(c *store.CloudConfig) {
			c.Enabled = true
			c.Provider = "google_drive"
			c.AccessToken = "at"
			c.ExpiryTimeMs = time.Now().UnixMilli() + 3600_000
			c.FolderID = "folder1"
		})
		svc.Endpoints.GoogleAPI = drive.URL
		svc.Endpoints.GoogleUpload = drive.URL

		if err := svc.Upload(src, "big__main__snap_1.zip"); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Errorf("reassembled %d bytes, want %d", got.Len(), len(payload))
		}
		if len(chunkRanges) != 4 {
			t.Errorf("chunks = %d (%v), want 4", len(chunkRanges), chunkRanges)
		}
	})

	t.Run("dropbox session multi-chunk", func(t *testing.T) {
		oldT, oldC := dropboxSessionThreshold, dropboxChunkSize
		dropboxSessionThreshold, dropboxChunkSize = 16<<10, 32<<10
		defer func() { dropboxSessionThreshold, dropboxChunkSize = oldT, oldC }()

		var got bytes.Buffer
		var calls []string
		content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.URL.Path)
			_, _ = io.Copy(&got, r.Body)
			if strings.HasSuffix(r.URL.Path, "/start") {
				_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "sess1"})
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{}`)
		}))
		defer content.Close()

		svc, s := newTestService(t)
		setCloudConfig(t, s, func(c *store.CloudConfig) {
			c.Enabled = true
			c.Provider = "dropbox"
			c.AccessToken = "at"
			c.ExpiryTimeMs = time.Now().UnixMilli() + 3600_000
		})
		svc.Endpoints.DropboxContent = content.URL

		if err := svc.Upload(src, "big__main__snap_2.zip"); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Errorf("reassembled %d bytes, want %d", got.Len(), len(payload))
		}
		if len(calls) < 4 || !strings.HasSuffix(calls[0], "/start") || !strings.HasSuffix(calls[len(calls)-1], "/finish") {
			t.Errorf("session call sequence = %v", calls)
		}
	})

	t.Run("onedrive session multi-chunk", func(t *testing.T) {
		oldL, oldC := onedriveSimpleLimit, onedriveChunkSize
		onedriveSimpleLimit, onedriveChunkSize = 16<<10, 32<<10
		defer func() { onedriveSimpleLimit, onedriveChunkSize = oldL, oldC }()

		var graphURL string
		var got bytes.Buffer
		chunks := 0
		graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/session-upload" {
				chunks++
				_, _ = io.Copy(&got, r.Body)
				if got.Len() == len(payload) {
					w.WriteHeader(http.StatusCreated)
				} else {
					w.WriteHeader(http.StatusAccepted)
				}
				fmt.Fprint(w, `{}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"uploadUrl": graphURL + "/session-upload"})
		}))
		defer graph.Close()
		graphURL = graph.URL

		svc, s := newTestService(t)
		setCloudConfig(t, s, func(c *store.CloudConfig) {
			c.Enabled = true
			c.Provider = "onedrive"
			c.AccessToken = "at"
			c.ExpiryTimeMs = time.Now().UnixMilli() + 3600_000
		})
		svc.Endpoints.Graph = graph.URL

		if err := svc.Upload(src, "big__main__snap_3.zip"); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Errorf("reassembled %d bytes, want %d", got.Len(), len(payload))
		}
		if chunks != 4 {
			t.Errorf("chunks = %d, want 4", chunks)
		}
	})
}
