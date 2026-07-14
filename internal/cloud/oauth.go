package cloud

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// redirectURI matches the registered OAuth apps (the auth popup intercepts
// navigation to it and extracts ?code=).
const redirectURI = "http://localhost/callback"

// Built-in public client IDs (overridable per-provider in settings).
var defaultClientIDs = map[string]string{
	"google_drive": decodeB64("MTU3NjU3NzQ0MTIwLTFuNG1oNmFoYzdkMThndHRxaW04ZTlpNmFjbzRhcm0zLmFwcHMuZ29vZ2xldXNlcmNvbnRlbnQuY29t"),
	"onedrive":     "", // users must supply their own Azure app registration
	"dropbox":      "myu2y05478whmk9",
}

func decodeB64(s string) string {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(raw)
}

// GeneratePKCE returns a fresh code verifier + S256 challenge pair.
func GeneratePKCE() (verifier, challenge string) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func (s *Service) clientID(provider string) (string, error) {
	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return "", err
	}
	if custom := strings.TrimSpace(cfg.CustomClientIDs[provider]); custom != "" {
		return custom, nil
	}
	if builtin := defaultClientIDs[provider]; builtin != "" {
		return builtin, nil
	}
	return "", fmt.Errorf("no OAuth Client ID available for %q — configure one under Settings > Cloud Backup", provider)
}

func (s *Service) clientSecret(provider string) string {
	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.CustomClientSecrets[provider])
}

// AuthURL builds the provider's authorization page URL for the PKCE flow.
func (s *Service) AuthURL(provider, codeChallenge string) (string, error) {
	clientID, err := s.clientID(provider)
	if err != nil {
		return "", err
	}

	params := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	switch provider {
	case "google_drive":
		params.Set("scope", "https://www.googleapis.com/auth/drive.file email openid")
		params.Set("access_type", "offline")
		params.Set("prompt", "consent")
		return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode(), nil
	case "dropbox":
		params.Set("token_access_type", "offline")
		params.Set("scope", "files.content.write files.content.read account_info.read")
		return "https://www.dropbox.com/oauth2/authorize?" + params.Encode(), nil
	case "onedrive":
		params.Set("scope", "Files.ReadWrite.AppFolder User.Read offline_access")
		return "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?" + params.Encode(), nil
	}
	return "", fmt.Errorf("unsupported cloud provider: %s", provider)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ExchangeAuthCode swaps an authorization code for tokens and persists
// them (with the account email) to the cloud config.
func (s *Service) ExchangeAuthCode(provider, code, codeVerifier string) error {
	clientID, err := s.clientID(provider)
	if err != nil {
		return err
	}
	secret := s.clientSecret(provider)

	var tok tokenResponse
	if provider == "google_drive" && secret == "" {
		// Default Google credentials: exchange through the relay's OAuth
		// proxy so the client secret stays server-side.
		tok, err = s.proxyTokenRequest(map[string]any{
			"provider": provider, "client_id": clientID,
			"grant_type": "authorization_code", "code": code,
			"code_verifier": codeVerifier, "redirect_uri": redirectURI,
		})
	} else {
		form := url.Values{
			"client_id":     {clientID},
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"code_verifier": {codeVerifier},
			"redirect_uri":  {redirectURI},
		}
		if secret != "" {
			form.Set("client_secret", secret)
		}
		tok, err = s.postTokenForm(s.tokenURL(provider), form)
	}
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Google's consent screen shows Drive access as an optional checkbox
	// (granular consent). If the user signed in without ticking it, the
	// token can't touch Drive — every upload would fail with
	// "insufficient permissions". Catch that here with a clear message
	// instead of persisting a useless token.
	if provider == "google_drive" && tok.Scope != "" && !strings.Contains(tok.Scope, "drive.file") {
		return fmt.Errorf("Google sign-in succeeded but Drive access wasn't granted — sign in again and TICK THE CHECKBOX allowing OpenSave to \"See, edit, create and delete only the specific Google Drive files that you use with this app\"")
	}

	email := s.fetchUserProfile(provider, tok.AccessToken)

	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return err
	}
	// Signing in IS choosing this provider: persist it so uploads work even
	// if the user never presses "Save settings", and so the stored tokens
	// always belong to the stored provider.
	cfg.Provider = provider
	cfg.AccessToken = tok.AccessToken
	cfg.RefreshToken = tok.RefreshToken
	cfg.ExpiryTimeMs = time.Now().UnixMilli() + tok.ExpiresIn*1000
	cfg.UserEmail = email
	return s.Store.UpdateCloudConfig(cfg)
}

// Disconnect wipes stored tokens.
func (s *Service) Disconnect() error {
	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return err
	}
	cfg.AccessToken = ""
	cfg.RefreshToken = ""
	cfg.ExpiryTimeMs = 0
	cfg.UserEmail = ""
	return s.Store.UpdateCloudConfig(cfg)
}

// getOrRefreshAccessToken returns a valid access token, refreshing (and
// persisting) when it expires within a minute.
func (s *Service) getOrRefreshAccessToken(provider string) (string, error) {
	cfg, err := s.Store.GetCloudConfig()
	if err != nil {
		return "", err
	}
	if cfg.AccessToken == "" {
		return "", fmt.Errorf("cloud sync not authenticated")
	}
	if cfg.ExpiryTimeMs > 0 && time.Now().UnixMilli() < cfg.ExpiryTimeMs-60_000 {
		return cfg.AccessToken, nil
	}
	if cfg.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token available; re-authentication required")
	}

	clientID, err := s.clientID(provider)
	if err != nil {
		return "", err
	}
	secret := s.clientSecret(provider)

	var tok tokenResponse
	if provider == "google_drive" && secret == "" {
		tok, err = s.proxyTokenRequest(map[string]any{
			"provider": provider, "client_id": clientID,
			"grant_type": "refresh_token", "refresh_token": cfg.RefreshToken,
		})
	} else {
		form := url.Values{
			"client_id":     {clientID},
			"grant_type":    {"refresh_token"},
			"refresh_token": {cfg.RefreshToken},
		}
		if secret != "" {
			form.Set("client_secret", secret)
		}
		tok, err = s.postTokenForm(s.tokenURL(provider), form)
	}
	if err != nil {
		// A dead refresh token (revoked, expired, or password-changed) can
		// never be recovered by retrying — the only fix is a fresh sign-in.
		// Wipe the stored credentials so the UI stops claiming "connected"
		// and prompts the user to reconnect, and return a plain message.
		if isInvalidGrant(err) {
			cfg.AccessToken = ""
			cfg.RefreshToken = ""
			cfg.ExpiryTimeMs = 0
			cfg.UserEmail = ""
			_ = s.Store.UpdateCloudConfig(cfg)
			return "", fmt.Errorf("your %s session has expired — reconnect under Cloud Backup to sign in again", providerLabel(provider))
		}
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	cfg.AccessToken = tok.AccessToken
	cfg.ExpiryTimeMs = time.Now().UnixMilli() + tok.ExpiresIn*1000
	if tok.RefreshToken != "" {
		cfg.RefreshToken = tok.RefreshToken
	}
	if err := s.Store.UpdateCloudConfig(cfg); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// isInvalidGrant reports whether a token error is Google/Dropbox/Microsoft's
// "the refresh token is permanently dead" signal.
func isInvalidGrant(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "token has been expired or revoked") ||
		strings.Contains(msg, "expired_token")
}

// providerLabel returns a human-friendly provider name for messages.
func providerLabel(provider string) string {
	switch provider {
	case "google_drive":
		return "Google Drive"
	case "dropbox":
		return "Dropbox"
	case "onedrive":
		return "OneDrive"
	}
	return provider
}

func (s *Service) tokenURL(provider string) string {
	switch provider {
	case "google_drive":
		return s.Endpoints.GoogleToken
	case "dropbox":
		return s.Endpoints.DropboxToken
	case "onedrive":
		return s.Endpoints.MicrosoftToken
	}
	return ""
}

// relayProxyURL derives the OAuth proxy endpoint from the configured relay
// (ws:// -> http://).
func (s *Service) relayProxyURL() string {
	settings, err := s.Store.GetSettings()
	if err != nil || settings.RelayURL == "" {
		return "https://opensave-relay.onrender.com/api/oauth/token"
	}
	httpURL := strings.Replace(strings.Replace(settings.RelayURL, "wss://", "https://", 1), "ws://", "http://", 1)
	return httpURL + "/api/oauth/token"
}

func (s *Service) proxyTokenRequest(payload map[string]any) (tokenResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return tokenResponse{}, err
	}
	resp, err := s.httpClient().Post(s.relayProxyURL(), "application/json", strings.NewReader(string(raw)))
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if strings.Contains(string(body), "invalid_client") {
			return tokenResponse{}, fmt.Errorf("the default Google Drive credentials are invalid or revoked — configure your own Client ID/Secret under Settings > Cloud Backup")
		}
		return tokenResponse{}, fmt.Errorf("HTTP %d - %s", resp.StatusCode, body)
	}
	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tokenResponse{}, err
	}
	return tok, nil
}

func (s *Service) postTokenForm(tokenURL string, form url.Values) (tokenResponse, error) {
	resp, err := s.httpClient().PostForm(tokenURL, form)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if strings.Contains(string(body), "invalid_client") {
			return tokenResponse{}, fmt.Errorf("the provider credentials are invalid or revoked — configure your own Client ID/Secret under Settings > Cloud Backup")
		}
		return tokenResponse{}, fmt.Errorf("HTTP %d - %s", resp.StatusCode, body)
	}
	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tokenResponse{}, err
	}
	return tok, nil
}

// fetchUserProfile best-effort resolves the account email for the
// settings UI.
func (s *Service) fetchUserProfile(provider, accessToken string) string {
	fallback := providerLabel(provider) + " account"
	var req *http.Request
	switch provider {
	case "google_drive":
		req, _ = http.NewRequest(http.MethodGet, s.Endpoints.GoogleUserInfo, nil)
	case "dropbox":
		req, _ = http.NewRequest(http.MethodPost, s.Endpoints.DropboxAPI+"/2/users/get_current_account", nil)
	case "onedrive":
		req, _ = http.NewRequest(http.MethodGet, s.Endpoints.Graph+"/v1.0/me", nil)
	default:
		return fallback
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fallback
	}

	var profile struct {
		Email             string `json:"email"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
		Name              struct {
			DisplayName string `json:"display_name"`
		} `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return fallback
	}
	for _, v := range []string{profile.Email, profile.Mail, profile.UserPrincipalName, profile.Name.DisplayName} {
		if v != "" {
			return v
		}
	}
	return fallback
}

// textprotoHeader builds a MIME header for multipart parts.
func textprotoHeader(key, value string) map[string][]string {
	return map[string][]string{key: {value}}
}
