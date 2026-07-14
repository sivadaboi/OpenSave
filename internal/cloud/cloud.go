// Package cloud implements snapshot backup to six providers — Google
// Drive, Dropbox, OneDrive, WebDAV, webhook, and a local folder — porting
// src/daemon/cloud.js. API base URLs are injectable so tests can stand in
// httptest servers for every provider.
package cloud

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// CloudFile is one remote snapshot entry.
type CloudFile struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"sizeBytes"`
	CreatedTime string `json:"createdTime"`
}

// Endpoints holds provider API bases; override in tests.
type Endpoints struct {
	GoogleAPI      string // https://www.googleapis.com
	GoogleUpload   string // https://www.googleapis.com (upload path shares the host)
	GoogleToken    string // https://oauth2.googleapis.com/token
	GoogleUserInfo string // https://www.googleapis.com/oauth2/v2/userinfo
	DropboxAPI     string // https://api.dropboxapi.com
	DropboxContent string // https://content.dropboxapi.com
	DropboxToken   string // https://api.dropbox.com/oauth2/token
	Graph          string // https://graph.microsoft.com
	MicrosoftToken string // https://login.microsoftonline.com/common/oauth2/v2.0/token
}

// DefaultEndpoints returns the production provider hosts.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		GoogleAPI:      "https://www.googleapis.com",
		GoogleUpload:   "https://www.googleapis.com",
		GoogleToken:    "https://oauth2.googleapis.com/token",
		GoogleUserInfo: "https://www.googleapis.com/oauth2/v2/userinfo",
		DropboxAPI:     "https://api.dropboxapi.com",
		DropboxContent: "https://content.dropboxapi.com",
		DropboxToken:   "https://api.dropbox.com/oauth2/token",
		Graph:          "https://graph.microsoft.com",
		MicrosoftToken: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	}
}

// Service performs cloud operations against the configured provider.
type Service struct {
	Store     *store.Store
	Log       func(level, msg string)
	Endpoints Endpoints
	HTTP      *http.Client
}

// New creates a production Service.
func New(s *store.Store, logf func(level, msg string)) *Service {
	return &Service{
		Store:     s,
		Log:       logf,
		Endpoints: DefaultEndpoints(),
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *Service) config() (store.CloudConfig, error) {
	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return store.CloudConfig{}, err
	}
	if !cfg.Enabled {
		return store.CloudConfig{}, fmt.Errorf("cloud sync is not enabled")
	}
	return cfg, nil
}

// Upload sends a snapshot zip to the configured provider. Errors are
// returned (the snapshot hook logs them without failing the snapshot).
func (s *Service) Upload(filePath, fileName string) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}
	s.Log("info", fmt.Sprintf("cloud: uploading %s via %s", fileName, strings.ToUpper(cfg.Provider)))

	raw, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %s", filePath)
	}

	switch cfg.Provider {
	case "local":
		if cfg.URL == "" {
			return fmt.Errorf("no local folder destination configured")
		}
		if err := os.MkdirAll(cfg.URL, 0o777); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.URL, fileName), raw, 0o666); err != nil {
			return err
		}

	case "webdav":
		if cfg.URL == "" {
			return fmt.Errorf("no destination URL configured")
		}
		req, err := http.NewRequest(http.MethodPut, joinURL(cfg.URL, url.PathEscape(fileName)), bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/zip")
		applyCustomHeaders(req, cfg.HeadersJSON)
		applyBasicAuth(req, cfg.Username, cfg.Password)
		if err := s.doOK(req); err != nil {
			return err
		}

	case "webhook":
		if cfg.URL == "" {
			return fmt.Errorf("no destination URL configured")
		}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			return err
		}
		if _, err := part.Write(raw); err != nil {
			return err
		}
		mw.Close()
		req, err := http.NewRequest(http.MethodPost, cfg.URL, &buf)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		applyCustomHeaders(req, cfg.HeadersJSON)
		if err := s.doOK(req); err != nil {
			return err
		}

	case "google_drive":
		token, err := s.getOrRefreshAccessToken("google_drive")
		if err != nil {
			return err
		}
		metadata := map[string]any{"name": fileName, "mimeType": "application/zip"}
		if cfg.FolderID != "" {
			metadata["parents"] = []string{cfg.FolderID}
		}
		metaRaw, _ := json.Marshal(metadata)

		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		metaPart, _ := mw.CreatePart(textprotoHeader("Content-Type", "application/json"))
		metaPart.Write(metaRaw)
		filePart, _ := mw.CreatePart(textprotoHeader("Content-Type", "application/zip"))
		filePart.Write(raw)
		mw.Close()

		req, err := http.NewRequest(http.MethodPost,
			s.Endpoints.GoogleUpload+"/upload/drive/v3/files?uploadType=multipart", &buf)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "multipart/related; boundary="+mw.Boundary())
		req.Header.Set("Authorization", "Bearer "+token)
		if err := s.doOK(req); err != nil {
			return googleDriveErr(err)
		}

	case "dropbox":
		token, err := s.getOrRefreshAccessToken("dropbox")
		if err != nil {
			return err
		}
		args, _ := json.Marshal(map[string]any{"path": "/OpenSave/" + fileName, "mode": "overwrite", "mute": true})
		req, err := http.NewRequest(http.MethodPost, s.Endpoints.DropboxContent+"/2/files/upload", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Dropbox-API-Arg", string(args))
		req.Header.Set("Content-Type", "application/octet-stream")
		if err := s.doOK(req); err != nil {
			return fmt.Errorf("Dropbox: %w", err)
		}

	case "onedrive":
		token, err := s.getOrRefreshAccessToken("onedrive")
		if err != nil {
			return err
		}
		uploadURL := s.Endpoints.Graph + "/v1.0/me/drive/special/approot:/" + url.PathEscape(fileName) + ":/content"
		req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/zip")
		if err := s.doOK(req); err != nil {
			return fmt.Errorf("OneDrive: %w", err)
		}

	default:
		return fmt.Errorf("unsupported cloud sync provider: %s", cfg.Provider)
	}

	s.Log("success", fmt.Sprintf("cloud: uploaded %q to %s", fileName, cfg.Provider))
	return nil
}

// List returns the provider's snapshot zips.
func (s *Service) List() ([]CloudFile, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}

	switch cfg.Provider {
	case "local":
		if cfg.URL == "" {
			return []CloudFile{}, nil
		}
		entries, err := os.ReadDir(cfg.URL)
		if err != nil {
			return []CloudFile{}, nil
		}
		var files []CloudFile
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, CloudFile{
				Name: e.Name(), SizeBytes: info.Size(),
				CreatedTime: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		return files, nil

	case "webdav":
		return s.listWebDAV(cfg)

	case "google_drive":
		token, err := s.getOrRefreshAccessToken("google_drive")
		if err != nil {
			return nil, err
		}
		query := "trashed = false and mimeType = 'application/zip'"
		if cfg.FolderID != "" {
			query += fmt.Sprintf(" and '%s' in parents", cfg.FolderID)
		}
		listURL := s.Endpoints.GoogleAPI + "/drive/v3/files?q=" + url.QueryEscape(query) + "&fields=" + url.QueryEscape("files(id,name,size,createdTime)")
		req, _ := http.NewRequest(http.MethodGet, listURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		var out struct {
			Files []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Size        string `json:"size"`
				CreatedTime string `json:"createdTime"`
			} `json:"files"`
		}
		if err := s.doJSON(req, &out); err != nil {
			return nil, googleDriveErr(err)
		}
		files := make([]CloudFile, len(out.Files))
		for i, f := range out.Files {
			size, _ := strconv.ParseInt(f.Size, 10, 64)
			files[i] = CloudFile{ID: f.ID, Name: f.Name, SizeBytes: size, CreatedTime: f.CreatedTime}
		}
		return files, nil

	case "dropbox":
		token, err := s.getOrRefreshAccessToken("dropbox")
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(map[string]string{"path": "/OpenSave"})
		req, _ := http.NewRequest(http.MethodPost, s.Endpoints.DropboxAPI+"/2/files/list_folder", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			return []CloudFile{}, nil // /OpenSave folder doesn't exist yet
		}
		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("Dropbox: HTTP %d - %s", resp.StatusCode, raw)
		}
		var out struct {
			Entries []struct {
				Tag            string `json:".tag"`
				Name           string `json:"name"`
				Size           int64  `json:"size"`
				ClientModified string `json:"client_modified"`
			} `json:"entries"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
		var files []CloudFile
		for _, e := range out.Entries {
			if e.Tag == "file" && strings.HasSuffix(e.Name, ".zip") {
				files = append(files, CloudFile{Name: e.Name, SizeBytes: e.Size, CreatedTime: e.ClientModified})
			}
		}
		return files, nil

	case "onedrive":
		token, err := s.getOrRefreshAccessToken("onedrive")
		if err != nil {
			return nil, err
		}
		req, _ := http.NewRequest(http.MethodGet, s.Endpoints.Graph+"/v1.0/me/drive/special/approot/children", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		var out struct {
			Value []struct {
				Name            string          `json:"name"`
				Size            int64           `json:"size"`
				CreatedDateTime string          `json:"createdDateTime"`
				File            json.RawMessage `json:"file"`
			} `json:"value"`
		}
		if err := s.doJSON(req, &out); err != nil {
			return nil, fmt.Errorf("OneDrive: %w", err)
		}
		var files []CloudFile
		for _, f := range out.Value {
			if f.File != nil && strings.HasSuffix(f.Name, ".zip") {
				files = append(files, CloudFile{Name: f.Name, SizeBytes: f.Size, CreatedTime: f.CreatedDateTime})
			}
		}
		return files, nil

	default:
		return []CloudFile{}, nil
	}
}

// Download fetches a remote snapshot to localPath.
func (s *Service) Download(fileName, localPath string) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}

	var raw []byte
	switch cfg.Provider {
	case "local":
		if cfg.URL == "" {
			return fmt.Errorf("no local folder destination configured")
		}
		src := filepath.Join(cfg.URL, fileName)
		raw, err = os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("file %q not found in local folder", fileName)
		}

	case "webdav":
		req, err := http.NewRequest(http.MethodGet, joinURL(cfg.URL, url.PathEscape(fileName)), nil)
		if err != nil {
			return err
		}
		applyBasicAuth(req, cfg.Username, cfg.Password)
		raw, err = s.fetchBytes(req)
		if err != nil {
			return fmt.Errorf("WebDAV: %w", err)
		}

	case "google_drive":
		token, err := s.getOrRefreshAccessToken("google_drive")
		if err != nil {
			return err
		}
		query := fmt.Sprintf("name = '%s' and trashed = false", strings.ReplaceAll(fileName, "'", `\'`))
		if cfg.FolderID != "" {
			query += fmt.Sprintf(" and '%s' in parents", cfg.FolderID)
		}
		listURL := s.Endpoints.GoogleAPI + "/drive/v3/files?q=" + url.QueryEscape(query) + "&fields=" + url.QueryEscape("files(id)")
		req, _ := http.NewRequest(http.MethodGet, listURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		var out struct {
			Files []struct {
				ID string `json:"id"`
			} `json:"files"`
		}
		if err := s.doJSON(req, &out); err != nil {
			return googleDriveErr(err)
		}
		if len(out.Files) == 0 {
			return fmt.Errorf("file %q not found on Google Drive", fileName)
		}
		dlReq, _ := http.NewRequest(http.MethodGet, s.Endpoints.GoogleAPI+"/drive/v3/files/"+out.Files[0].ID+"?alt=media", nil)
		dlReq.Header.Set("Authorization", "Bearer "+token)
		raw, err = s.fetchBytes(dlReq)
		if err != nil {
			return googleDriveErr(err)
		}

	case "dropbox":
		token, err := s.getOrRefreshAccessToken("dropbox")
		if err != nil {
			return err
		}
		args, _ := json.Marshal(map[string]string{"path": "/OpenSave/" + fileName})
		req, _ := http.NewRequest(http.MethodPost, s.Endpoints.DropboxContent+"/2/files/download", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Dropbox-API-Arg", string(args))
		raw, err = s.fetchBytes(req)
		if err != nil {
			return fmt.Errorf("Dropbox: %w", err)
		}

	case "onedrive":
		token, err := s.getOrRefreshAccessToken("onedrive")
		if err != nil {
			return err
		}
		dlURL := s.Endpoints.Graph + "/v1.0/me/drive/special/approot:/" + url.PathEscape(fileName) + ":/content"
		req, _ := http.NewRequest(http.MethodGet, dlURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		raw, err = s.fetchBytes(req)
		if err != nil {
			return fmt.Errorf("OneDrive: %w", err)
		}

	default:
		return fmt.Errorf("downloading is not supported for provider: %s", cfg.Provider)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o777); err != nil {
		return err
	}
	return os.WriteFile(localPath, raw, 0o666)
}

// listWebDAV issues a Depth-1 PROPFIND and parses the multistatus XML.
func (s *Service) listWebDAV(cfg store.CloudConfig) ([]CloudFile, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("no destination URL configured")
	}
	req, err := http.NewRequest("PROPFIND", cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "text/xml")
	applyBasicAuth(req, cfg.Username, cfg.Password)

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebDAV list returned HTTP %d", resp.StatusCode)
	}

	var ms struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Length   string `xml:"prop>getcontentlength"`
				Modified string `xml:"prop>getlastmodified"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("parse WebDAV multistatus: %w", err)
	}

	baseName := path.Base(strings.TrimSuffix(cfg.URL, "/"))
	var files []CloudFile
	for _, r := range ms.Responses {
		href, err := url.PathUnescape(strings.TrimSpace(r.Href))
		if err != nil {
			href = r.Href
		}
		name := path.Base(strings.TrimSuffix(href, "/"))
		if name == "" || name == baseName {
			continue
		}
		f := CloudFile{Name: name, CreatedTime: time.Now().UTC().Format(time.RFC3339)}
		for _, p := range r.Props {
			if p.Length != "" {
				f.SizeBytes, _ = strconv.ParseInt(strings.TrimSpace(p.Length), 10, 64)
			}
			if p.Modified != "" {
				if t, err := time.Parse(time.RFC1123, strings.TrimSpace(p.Modified)); err == nil {
					f.CreatedTime = t.UTC().Format(time.RFC3339)
				}
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// Delete removes one remote snapshot. Webhook destinations are fire-and-
// forget and don't support deletion.
func (s *Service) Delete(f CloudFile) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}

	switch cfg.Provider {
	case "local":
		if cfg.URL == "" {
			return fmt.Errorf("no local folder destination configured")
		}
		return os.Remove(filepath.Join(cfg.URL, f.Name))

	case "webdav":
		req, err := http.NewRequest(http.MethodDelete, joinURL(cfg.URL, url.PathEscape(f.Name)), nil)
		if err != nil {
			return err
		}
		applyCustomHeaders(req, cfg.HeadersJSON)
		applyBasicAuth(req, cfg.Username, cfg.Password)
		return s.doOK(req)

	case "google_drive":
		token, err := s.getOrRefreshAccessToken("google_drive")
		if err != nil {
			return err
		}
		id := f.ID
		if id == "" {
			return fmt.Errorf("missing Drive file id for %s", f.Name)
		}
		req, _ := http.NewRequest(http.MethodDelete, s.Endpoints.GoogleAPI+"/drive/v3/files/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := s.doOK(req); err != nil {
			return googleDriveErr(err)
		}
		return nil

	case "dropbox":
		token, err := s.getOrRefreshAccessToken("dropbox")
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"path": "/OpenSave/" + f.Name})
		req, _ := http.NewRequest(http.MethodPost, s.Endpoints.DropboxAPI+"/2/files/delete_v2", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if err := s.doOK(req); err != nil {
			return fmt.Errorf("Dropbox: %w", err)
		}
		return nil

	case "onedrive":
		token, err := s.getOrRefreshAccessToken("onedrive")
		if err != nil {
			return err
		}
		req, _ := http.NewRequest(http.MethodDelete, s.Endpoints.Graph+"/v1.0/me/drive/special/approot:/"+url.PathEscape(f.Name), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := s.doOK(req); err != nil {
			return fmt.Errorf("OneDrive: %w", err)
		}
		return nil
	}
	return fmt.Errorf("deletion is not supported for provider: %s", cfg.Provider)
}

// PruneGameBranch keeps the newest `keep` remote snapshots of one game
// branch and deletes the rest — the cloud-side mirror of local snapshot
// retention. matchName reports whether a remote file belongs to the
// game+branch (the caller owns the naming scheme). keep <= 0 disables
// pruning.
func (s *Service) PruneGameBranch(matchName func(string) bool, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	files, err := s.List()
	if err != nil {
		return 0, err
	}
	var matches []CloudFile
	for _, f := range files {
		if matchName(f.Name) {
			matches = append(matches, f)
		}
	}
	if len(matches) <= keep {
		return 0, nil
	}
	// Newest first; delete everything past `keep`.
	sortByCreatedDesc(matches)
	pruned := 0
	for _, f := range matches[keep:] {
		if err := s.Delete(f); err != nil {
			s.Log("warn", fmt.Sprintf("cloud: prune of %s failed: %v", f.Name, err))
			continue
		}
		pruned++
	}
	if pruned > 0 {
		s.Log("info", fmt.Sprintf("cloud: pruned %d old snapshot(s) beyond retention of %d", pruned, keep))
	}
	return pruned, nil
}

func sortByCreatedDesc(files []CloudFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].CreatedTime > files[j-1].CreatedTime; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}

// googleDriveErr wraps Drive API failures; a 403 "insufficient permissions"
// means the account was connected without ticking the Drive checkbox on
// Google's consent screen, so tell the user exactly how to fix it.
func googleDriveErr(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "insufficient") && (strings.Contains(msg, "403") || strings.Contains(msg, "permission")) {
		return fmt.Errorf("Google Drive access was not granted for this account — open Cloud Backup, Disconnect, then sign in again and TICK THE CHECKBOX that allows OpenSave to access its own Drive files")
	}
	return fmt.Errorf("Google Drive: %w", err)
}

func (s *Service) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *Service) doOK(req *http.Request) error {
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d - %s", resp.StatusCode, raw)
	}
	return nil
}

func (s *Service) doJSON(req *http.Request, out any) error {
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d - %s", resp.StatusCode, raw)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *Service) fetchBytes(req *http.Request) ([]byte, error) {
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d - %s", resp.StatusCode, raw)
	}
	return io.ReadAll(resp.Body)
}

func joinURL(base, name string) string {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + name
}

func applyBasicAuth(req *http.Request, username, password string) {
	if username != "" || password != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+cred)
	}
}

func applyCustomHeaders(req *http.Request, headersJSON string) {
	if headersJSON == "" || headersJSON == "{}" {
		return
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}
