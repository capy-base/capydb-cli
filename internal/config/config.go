package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultAPIURL = "https://capydb.dev/api/capydb"
const defaultAppURL = "https://capydb.dev"

// DefaultOrgKey keys credentials that were saved without a known organization
// id (e.g. a manually pasted API key whose viewer has no organization).
const DefaultOrgKey = "default"

type ProjectConfig struct {
	AppPath        string `json:"app_path,omitempty"`
	APIURL         string `json:"api_url"`
	DatabaseLayer  string `json:"database_layer,omitempty"`
	Region         string `json:"region,omitempty"`
	DatabaseURLVar string `json:"database_url_var"`
	DirectURLVar   string `json:"direct_url_var,omitempty"`
	EnvFile        string `json:"env_file"`
	Framework      string `json:"framework"`
	OrganizationID string `json:"organization_id,omitempty"`
	PooledURLVar   string `json:"pooled_url_var,omitempty"`
	Profile        string `json:"profile,omitempty"`
	ProjectID      string `json:"project_id"`
	ProjectName    string `json:"project_name"`
	ProjectSlug    string `json:"project_slug,omitempty"`
}

// OrganizationConfig holds the saved credentials and endpoints for one
// organization the CLI has logged in to.
type OrganizationConfig struct {
	APIKey string `json:"api_key"`
	APIURL string `json:"api_url"`
	AppURL string `json:"app_url,omitempty"`
	Name   string `json:"name,omitempty"`
	Slug   string `json:"slug,omitempty"`
}

// UserConfig stores credentials per organization, keyed by organization id,
// with ActiveOrg selecting the entry everyday commands use.
type UserConfig struct {
	ActiveOrg     string                        `json:"active_org,omitempty"`
	Organizations map[string]OrganizationConfig `json:"organizations,omitempty"`
}

// legacyUserConfig is the pre-multi-org single-credential config shape. It is
// migrated to UserConfig once on load and never written back.
type legacyUserConfig struct {
	APIKey           string `json:"api_key"`
	APIURL           string `json:"api_url"`
	AppURL           string `json:"app_url,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	OrganizationName string `json:"organization_name,omitempty"`
	OrganizationSlug string `json:"organization_slug,omitempty"`
}

// Active returns the active organization id and its credentials. ok is false
// when no usable entry exists.
func (c UserConfig) Active() (string, OrganizationConfig, bool) {
	if len(c.Organizations) == 0 {
		return "", OrganizationConfig{}, false
	}
	if entry, found := c.Organizations[c.ActiveOrg]; found {
		return c.ActiveOrg, entry, true
	}
	// Self-heal a dangling active_org when exactly one entry exists.
	if len(c.Organizations) == 1 {
		for orgID, entry := range c.Organizations {
			return orgID, entry, true
		}
	}
	return "", OrganizationConfig{}, false
}

// APIKey returns the active organization's API key, or "".
func (c UserConfig) APIKey() string {
	_, entry, ok := c.Active()
	if !ok {
		return ""
	}
	return entry.APIKey
}

// APIURL returns the active organization's API URL, or the default.
func (c UserConfig) APIURL() string {
	_, entry, ok := c.Active()
	if !ok || strings.TrimSpace(entry.APIURL) == "" {
		return DefaultAPIURL()
	}
	return trimURL(entry.APIURL)
}

// AppURL returns the active organization's app URL, derived from the API URL
// when unset.
func (c UserConfig) AppURL() string {
	_, entry, ok := c.Active()
	if !ok || strings.TrimSpace(entry.AppURL) == "" {
		return DefaultAppURL(c.APIURL())
	}
	return trimURL(entry.AppURL)
}

// Upsert adds or replaces the credentials for orgID and makes it the active
// organization. An empty orgID is stored under DefaultOrgKey.
func (c *UserConfig) Upsert(orgID string, entry OrganizationConfig) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		orgID = DefaultOrgKey
	}
	if c.Organizations == nil {
		c.Organizations = map[string]OrganizationConfig{}
	}
	entry.APIURL = trimURL(firstNonEmpty(entry.APIURL, DefaultAPIURL()))
	entry.AppURL = trimURL(firstNonEmpty(entry.AppURL, DefaultAppURL(entry.APIURL)))
	c.Organizations[orgID] = entry
	c.ActiveOrg = orgID
}

func DefaultAPIURL() string {
	if value := strings.TrimSpace(os.Getenv("CAPYDB_API_URL")); value != "" {
		return trimURL(value)
	}
	return defaultAPIURL
}

func DefaultAppURL(apiURL string) string {
	if value := strings.TrimSpace(os.Getenv("CAPYDB_APP_URL")); value != "" {
		return trimURL(value)
	}
	apiURL = trimURL(apiURL)
	if before, ok := strings.CutSuffix(apiURL, "/api/capydb"); ok {
		return before
	}
	if apiURL != "" {
		return apiURL
	}
	return defaultAppURL
}

func LoadProjectConfig(cwd string) (ProjectConfig, error) {
	path := ProjectConfigPath(cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ProjectConfig{}, os.ErrNotExist
		}
		return ProjectConfig{}, fmt.Errorf("read project config: %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("decode project config: %w", err)
	}

	cfg.APIURL = trimURL(firstNonEmpty(cfg.APIURL, DefaultAPIURL()))
	return cfg, nil
}

// LoadUserConfig reads the user config. A legacy single-credential file is
// migrated to the per-organization shape and written back once.
func LoadUserConfig() (UserConfig, error) {
	path, err := UserConfigPath()
	if err != nil {
		return UserConfig{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return UserConfig{}, nil
		}
		return UserConfig{}, fmt.Errorf("read user config: %w", err)
	}

	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{}, fmt.Errorf("decode user config: %w", err)
	}
	if len(cfg.Organizations) > 0 {
		normalize(&cfg)
		return cfg, nil
	}

	// Legacy shape: a flat single-credential object. Convert and persist the
	// new shape so the migration runs exactly once.
	var legacy legacyUserConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return UserConfig{}, fmt.Errorf("decode user config: %w", err)
	}
	if strings.TrimSpace(legacy.APIKey) == "" {
		return UserConfig{}, nil
	}

	cfg = UserConfig{}
	cfg.Upsert(legacy.OrganizationID, OrganizationConfig{
		APIKey: strings.TrimSpace(legacy.APIKey),
		APIURL: legacy.APIURL,
		AppURL: legacy.AppURL,
		Name:   legacy.OrganizationName,
		Slug:   legacy.OrganizationSlug,
	})
	if err := SaveUserConfig(cfg); err != nil {
		return UserConfig{}, fmt.Errorf("migrate user config: %w", err)
	}
	return cfg, nil
}

func normalize(cfg *UserConfig) {
	for orgID, entry := range cfg.Organizations {
		entry.APIURL = trimURL(firstNonEmpty(entry.APIURL, DefaultAPIURL()))
		entry.AppURL = trimURL(firstNonEmpty(entry.AppURL, DefaultAppURL(entry.APIURL)))
		cfg.Organizations[orgID] = entry
	}
	if _, found := cfg.Organizations[cfg.ActiveOrg]; !found && len(cfg.Organizations) == 1 {
		for orgID := range cfg.Organizations {
			cfg.ActiveOrg = orgID
		}
	}
}

func ProjectConfigPath(cwd string) string {
	return filepath.Join(cwd, ".capydb", "project.json")
}

func SaveProjectConfig(cwd string, cfg ProjectConfig) error {
	path := ProjectConfigPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .capydb directory: %w", err)
	}

	cfg.APIURL = trimURL(firstNonEmpty(cfg.APIURL, DefaultAPIURL()))
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

func SaveUserConfig(cfg UserConfig) error {
	path, err := UserConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	normalize(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write user config: %w", err)
	}
	return nil
}

func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "capydb", "config.json"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func trimURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}
