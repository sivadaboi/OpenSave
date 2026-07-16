package store

import (
	"encoding/json"
	"fmt"
)

// CloudConfig is the singleton cloud-backup configuration row, including
// the current provider's OAuth tokens. It mirrors the JS app's
// settings.cloudSync object, flattened (tokens.* -> top-level columns).
type CloudConfig struct {
	ID                  int               `db:"id" json:"-"`
	Enabled             bool              `db:"enabled" json:"enabled"`
	Provider            string            `db:"provider" json:"provider"`
	URL                 string            `db:"url" json:"url"`
	Username            string            `db:"username" json:"username"`
	Password            string            `db:"password" json:"password"`
	HeadersJSON         string            `db:"headers_json" json:"headers"`
	FolderID            string            `db:"folder_id" json:"folderId"`
	CustomClientIDs     map[string]string `db:"-" json:"customClientIds"`
	CustomClientSecrets map[string]string `db:"-" json:"customClientSecrets"`
	AccessToken         string            `db:"access_token" json:"-"`
	RefreshToken        string            `db:"refresh_token" json:"-"`
	ExpiryTimeMs        int64             `db:"expiry_time_ms" json:"-"`
	UserEmail           string            `db:"user_email" json:"-"`

	CustomClientIDsJSON     string `db:"custom_client_ids" json:"-"`
	CustomClientSecretsJSON string `db:"custom_client_secrets" json:"-"`
}

// GetCloudConfig returns the singleton cloud config row.
func (s *Store) GetCloudConfig() (CloudConfig, error) {
	var c CloudConfig
	if err := s.db.Get(&c, `SELECT * FROM cloud_config WHERE id = 1`); err != nil {
		return CloudConfig{}, fmt.Errorf("get cloud config: %w", err)
	}
	if c.CustomClientIDsJSON != "" {
		if err := json.Unmarshal([]byte(c.CustomClientIDsJSON), &c.CustomClientIDs); err != nil {
			return CloudConfig{}, fmt.Errorf("unmarshal customClientIds: %w", err)
		}
	}
	if c.CustomClientSecretsJSON != "" {
		if err := json.Unmarshal([]byte(c.CustomClientSecretsJSON), &c.CustomClientSecrets); err != nil {
			return CloudConfig{}, fmt.Errorf("unmarshal customClientSecrets: %w", err)
		}
	}
	return c, nil
}

// UpdateCloudConfig persists the given config as the new singleton row.
func (s *Store) UpdateCloudConfig(c CloudConfig) error {
	if c.CustomClientIDs == nil {
		c.CustomClientIDs = map[string]string{}
	}
	if c.CustomClientSecrets == nil {
		c.CustomClientSecrets = map[string]string{}
	}
	idsJSON, err := json.Marshal(c.CustomClientIDs)
	if err != nil {
		return err
	}
	secretsJSON, err := json.Marshal(c.CustomClientSecrets)
	if err != nil {
		return err
	}
	c.CustomClientIDsJSON = string(idsJSON)
	c.CustomClientSecretsJSON = string(secretsJSON)

	_, err = s.db.NamedExec(`
		UPDATE cloud_config SET
			enabled = :enabled,
			provider = :provider,
			url = :url,
			username = :username,
			password = :password,
			headers_json = :headers_json,
			folder_id = :folder_id,
			custom_client_ids = :custom_client_ids,
			custom_client_secrets = :custom_client_secrets,
			access_token = :access_token,
			refresh_token = :refresh_token,
			expiry_time_ms = :expiry_time_ms,
			user_email = :user_email
		WHERE id = 1`, c)
	if err != nil {
		return fmt.Errorf("update cloud config: %w", err)
	}
	return nil
}
