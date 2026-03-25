package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultDownloadConcurrency = 4

type Config struct {
	AccountID           string `json:"account_id"`
	AccessKeyID         string `json:"access_key_id"`
	SecretAccessKey     string `json:"secret_access_key"`
	APIToken            string `json:"api_token,omitempty"`
	DownloadConcurrency int    `json:"download_concurrency,omitempty"`
	InlinePreviews      bool   `json:"inline_previews,omitempty"`
	ViewMode            string `json:"view_mode,omitempty"` // "list" | "grid"
	DeleteEnabled       bool   `json:"delete_enabled,omitempty"`
}

// configPath returns ~/.config/artoo/config.json
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "artoo", "config.json"), nil
}

// HasConfig checks if saved credentials exist on disk.
func (a *App) HasConfig() bool {
	p, err := configPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// GetConfig returns the current in-memory configuration so the frontend can
// pre-fill the settings form. Returns nil if no config has been loaded.
func (a *App) GetConfig() *Config {
	return a.config
}

// LoadConfig reads saved credentials from disk and initializes the S3 client.
func (a *App) LoadConfig() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	a.config = &cfg
	a.initClient()
	return nil
}

// SaveConfig persists credentials and re-initializes the S3 client.
// Preserves any existing DownloadConcurrency setting.
func (a *App) SaveConfig(accountID, accessKeyID, secretAccessKey, apiToken string) error {
	conc := defaultDownloadConcurrency
	if a.config != nil && a.config.DownloadConcurrency > 0 {
		conc = a.config.DownloadConcurrency
	}
	inlinePreviews := false
	if a.config != nil {
		inlinePreviews = a.config.InlinePreviews
	}
	cfg := Config{
		AccountID:           strings.TrimSpace(accountID),
		AccessKeyID:         strings.TrimSpace(accessKeyID),
		SecretAccessKey:     strings.TrimSpace(secretAccessKey),
		APIToken:            strings.TrimSpace(apiToken),
		DownloadConcurrency: conc,
		InlinePreviews:      inlinePreviews,
	}

	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		return err
	}

	a.config = &cfg
	a.initClient()
	return nil
}

// ClearConfig removes saved credentials from disk and resets the client.
func (a *App) ClearConfig() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	a.client = nil
	a.config = nil
	return os.Remove(p)
}

const maxConcurrencyLimit = 100

// maxConcurrency returns the cap for parallel downloads.
func maxConcurrency() int { return maxConcurrencyLimit }

// GetDownloadConcurrency returns the current setting and the maximum allowed value.
func (a *App) GetDownloadConcurrency() (current, max int) {
	max = maxConcurrency()
	if a.config != nil && a.config.DownloadConcurrency > 0 {
		current = a.config.DownloadConcurrency
	} else {
		current = defaultDownloadConcurrency
	}
	return
}

// SetViewMode persists the view mode ("list" or "grid").
func (a *App) SetViewMode(mode string) error {
	if mode != "list" && mode != "grid" {
		return fmt.Errorf("invalid view mode: %s", mode)
	}
	if a.config == nil {
		return fmt.Errorf("not connected")
	}
	a.config.ViewMode = mode
	p, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// DeleteAllowed returns whether delete operations are currently permitted.
func (a *App) DeleteAllowed() bool {
	return a.config != nil && a.config.DeleteEnabled
}

// SetDeleteEnabled persists the delete lock setting.
func (a *App) SetDeleteEnabled(enabled bool) error {
	if a.config == nil {
		return fmt.Errorf("not connected")
	}
	a.config.DeleteEnabled = enabled
	p, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// SetInlinePreviews persists the inline image preview toggle.
func (a *App) SetInlinePreviews(enabled bool) error {
	if a.config == nil {
		return fmt.Errorf("not connected")
	}
	a.config.InlinePreviews = enabled
	p, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// SetDownloadConcurrency persists a new concurrency value, clamped to [1, GOMAXPROCS*2].
func (a *App) SetDownloadConcurrency(n int) error {
	if n < 1 {
		n = 1
	}
	if cap := maxConcurrency(); n > cap {
		n = cap
	}
	if a.config == nil {
		return fmt.Errorf("not connected")
	}
	a.config.DownloadConcurrency = n

	p, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}
