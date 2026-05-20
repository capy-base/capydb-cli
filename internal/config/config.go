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

type ProjectConfig struct {
	AppPath        string `json:"app_path,omitempty"`
	APIURL         string `json:"api_url"`
	DatabaseLayer  string `json:"database_layer,omitempty"`
	ClusterID      string `json:"cluster_id"`
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

type UserConfig struct {
	APIKey           string `json:"api_key"`
	APIURL           string `json:"api_url"`
	AppURL           string `json:"app_url,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	OrganizationName string `json:"organization_name,omitempty"`
	OrganizationSlug string `json:"organization_slug,omitempty"`
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
	if strings.HasSuffix(apiURL, "/api/capydb") {
		return strings.TrimSuffix(apiURL, "/api/capydb")
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

func LoadUserConfig() (UserConfig, error) {
	path, err := UserConfigPath()
	if err != nil {
		return UserConfig{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return UserConfig{APIURL: DefaultAPIURL()}, nil
		}
		return UserConfig{}, fmt.Errorf("read user config: %w", err)
	}

	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{}, fmt.Errorf("decode user config: %w", err)
	}

	cfg.APIURL = trimURL(firstNonEmpty(cfg.APIURL, DefaultAPIURL()))
	cfg.AppURL = trimURL(firstNonEmpty(cfg.AppURL, DefaultAppURL(cfg.APIURL)))
	return cfg, nil
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

	cfg.APIURL = trimURL(firstNonEmpty(cfg.APIURL, DefaultAPIURL()))
	cfg.AppURL = trimURL(firstNonEmpty(cfg.AppURL, DefaultAppURL(cfg.APIURL)))
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
