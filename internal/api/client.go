package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type APIError struct {
	Message    string
	StatusCode int
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("api request failed with status %d: %s", e.StatusCode, e.Message)
}

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type Cluster struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Region     string `json:"region"`
	PublicHost string `json:"public_host"`
	DirectPort int    `json:"direct_port"`
	PooledPort int    `json:"pooled_port"`
	State      string `json:"state"`
}

type CreateProjectRequest struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
	Region    string `json:"region,omitempty"`
	Slug      string `json:"slug,omitempty"`
}

type Organization struct {
	BillingPeriodEnd *time.Time `json:"billing_period_end,omitempty"`
	BillingPlan      string     `json:"billing_plan"`
	BillingStatus    string     `json:"billing_status"`
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
}

type Viewer struct {
	Organization *Organization `json:"organization"`
}

type ViewerPrincipal struct {
	AuthSource            string   `json:"auth_source"`
	ClerkOrganizationID   string   `json:"clerk_organization_id,omitempty"`
	ClerkOrganizationRole string   `json:"clerk_organization_role,omitempty"`
	ClerkOrganizationSlug string   `json:"clerk_organization_slug,omitempty"`
	IsAdmin               bool     `json:"is_admin"`
	OrganizationID        string   `json:"organization_id,omitempty"`
	Scopes                []string `json:"scopes"`
	UserID                string   `json:"user_id,omitempty"`
}

type ViewerResponse struct {
	Organization *Organization   `json:"organization"`
	Principal    ViewerPrincipal `json:"principal"`
}

type CLILoginSessionStart struct {
	ExpiresAt time.Time `json:"expires_at"`
	PollToken string    `json:"poll_token"`
	SessionID string    `json:"session_id"`
	State     string    `json:"state"`
}

type CLILoginSessionStartRequest struct {
	DeviceName string     `json:"device_name,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type CLILoginSessionStatus struct {
	AuthorizedAt     *time.Time `json:"authorized_at,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	OrganizationID   string     `json:"organization_id,omitempty"`
	OrganizationName string     `json:"organization_name,omitempty"`
	OrganizationSlug string     `json:"organization_slug,omitempty"`
	PlaintextAPIKey  string     `json:"plaintext_api_key,omitempty"`
	SessionID        string     `json:"session_id"`
	State            string     `json:"state"`
}

type CreatePreviewRequest struct {
	Mode     string `json:"mode,omitempty"`
	Name     string `json:"name,omitempty"`
	TTLHours int    `json:"ttl_hours,omitempty"`
}

type CreateRestoreRequest struct {
	BackupKey               string `json:"backup_key,omitempty"`
	ConfirmProjectOverwrite bool   `json:"confirm_project_overwrite,omitempty"`
	PreviewID               string `json:"preview_id,omitempty"`
	PreviewName             string `json:"preview_name,omitempty"`
	Recreate                bool   `json:"recreate,omitempty"`
	RestoreTime             string `json:"restore_time,omitempty"`
	TargetKind              string `json:"target_kind,omitempty"`
	TTLHours                int    `json:"ttl_hours,omitempty"`
}

type Job struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

type Project struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	ClusterID      string `json:"cluster_id"`
	Environment    string `json:"environment,omitempty"`
	Plan           string `json:"plan,omitempty"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Region         string `json:"region,omitempty"`
	State          string `json:"state"`
	LastError      string `json:"last_error,omitempty"`
	LatestJobID    string `json:"latest_job_id,omitempty"`
}

type ConnectionInfo struct {
	DirectURL string `json:"direct_url,omitempty"`
	PooledURL string `json:"pooled_url,omitempty"`
	Username  string `json:"username"`
}

type ProjectConnectionInfo = ConnectionInfo

type PreviewConnectionInfo = ConnectionInfo

type Preview struct {
	CreatedAt      time.Time `json:"created_at"`
	DatabaseName   string    `json:"database_name"`
	DirectPort     int       `json:"direct_port"`
	ID             string    `json:"id"`
	LastError      string    `json:"last_error,omitempty"`
	Mode           string    `json:"mode"`
	Name           string    `json:"name"`
	PooledPort     int       `json:"pooled_port"`
	ProjectID      string    `json:"project_id"`
	PublicHost     string    `json:"public_host,omitempty"`
	RoleName       string    `json:"role_name"`
	SourceDatabase string    `json:"source_database,omitempty"`
	SSLMode        string    `json:"ssl_mode,omitempty"`
	State          string    `json:"state"`
	TTLExpiresAt   time.Time `json:"ttl_expires_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PreviewDetails struct {
	Connections PreviewConnectionInfo `json:"connections"`
	Preview     Preview               `json:"preview"`
}

type Backup struct {
	BackupKey         string    `json:"backup_key"`
	CreatedAt         time.Time `json:"created_at"`
	DatabaseName      string    `json:"database_name"`
	ID                string    `json:"id"`
	Label             string    `json:"label,omitempty"`
	ProjectID         string    `json:"project_id"`
	SizeBytes         int64     `json:"size_bytes"`
	State             string    `json:"state"`
	VerificationState string    `json:"verification_state"`
}

type PublicStatusComponent struct {
	Message string `json:"message,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
}

type PublicStatusResponse struct {
	Components []PublicStatusComponent `json:"components"`
	Service    string                  `json:"service"`
	Status     string                  `json:"status"`
	UpdatedAt  time.Time               `json:"updated_at"`
}

type ProjectObservability struct {
	Alerts                 []string `json:"alerts"`
	ConnectionCount        int      `json:"connection_count"`
	ConnectionLimit        int      `json:"connection_limit"`
	ConnectionUsagePercent float64  `json:"connection_usage_percent"`
	DatabaseSizeBytes      int64    `json:"database_size_bytes"`
	StorageLimitBytes      int64    `json:"storage_limit_bytes"`
	StorageUsagePercent    float64  `json:"storage_usage_percent"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) GetPublicStatus(ctx context.Context) (PublicStatusResponse, error) {
	var response PublicStatusResponse
	if err := c.do(ctx, http.MethodGet, "/status", nil, &response); err != nil {
		return PublicStatusResponse{}, err
	}
	return response, nil
}

func (c *Client) CreateProject(ctx context.Context, request CreateProjectRequest) (Project, Job, error) {
	var response struct {
		Job     Job     `json:"job"`
		Project Project `json:"project"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/projects", request, &response); err != nil {
		return Project{}, Job{}, err
	}
	return response.Project, response.Job, nil
}

func (c *Client) GetViewer(ctx context.Context) (Viewer, error) {
	var response Viewer
	if err := c.do(ctx, http.MethodGet, "/v1/me", nil, &response); err != nil {
		return Viewer{}, err
	}
	return response, nil
}

func (c *Client) GetViewerResponse(ctx context.Context) (ViewerResponse, error) {
	var response ViewerResponse
	if err := c.do(ctx, http.MethodGet, "/v1/me", nil, &response); err != nil {
		return ViewerResponse{}, err
	}
	return response, nil
}

func (c *Client) StartCLILoginSession(ctx context.Context, request CLILoginSessionStartRequest) (CLILoginSessionStart, error) {
	var response CLILoginSessionStart
	if err := c.do(ctx, http.MethodPost, "/v1/cli/login/sessions", request, &response); err != nil {
		return CLILoginSessionStart{}, err
	}
	return response, nil
}

func (c *Client) PollCLILoginSession(ctx context.Context, sessionID, pollToken string) (CLILoginSessionStatus, error) {
	var response CLILoginSessionStatus
	path := "/v1/cli/login/sessions/" + strings.TrimSpace(sessionID)
	if trimmed := strings.TrimSpace(pollToken); trimmed != "" {
		values := url.Values{}
		values.Set("poll_token", trimmed)
		path += "?" + values.Encode()
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return CLILoginSessionStatus{}, err
	}
	return response, nil
}

func (c *Client) CreateBackup(ctx context.Context, projectID, label string) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	payload := map[string]string{}
	if strings.TrimSpace(label) != "" {
		payload["label"] = strings.TrimSpace(label)
	}
	var body any
	if len(payload) > 0 {
		body = payload
	}
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/backups", body, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) CreateImport(ctx context.Context, projectID, sourceURL string, recreate bool) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/imports", map[string]any{
		"recreate":   recreate,
		"source_url": strings.TrimSpace(sourceURL),
	}, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) CreatePreviewDatabase(ctx context.Context, projectID string, request CreatePreviewRequest) (PreviewDetails, Job, error) {
	var response struct {
		Job     Job     `json:"job"`
		Preview Preview `json:"preview"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/preview-databases", request, &response); err != nil {
		return PreviewDetails{}, Job{}, err
	}

	details := PreviewDetails{Preview: response.Preview}
	connections, err := c.GetPreviewConnection(ctx, response.Preview.ID)
	if err == nil {
		details.Connections = connections
	}
	return details, response.Job, nil
}

func (c *Client) CreateRestore(ctx context.Context, projectID string, request CreateRestoreRequest) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/restores", request, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) DeletePreviewDatabase(ctx context.Context, previewID string) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	if err := c.do(ctx, http.MethodDelete, "/v1/preview-databases/"+previewID, nil, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) GetJob(ctx context.Context, jobID string) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/jobs/"+jobID, nil, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (Project, ProjectConnectionInfo, error) {
	var response struct {
		Project Project `json:"project"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID, nil, &response); err != nil {
		return Project{}, ProjectConnectionInfo{}, err
	}
	return response.Project, ProjectConnectionInfo{}, nil
}

func (c *Client) GetProjectConnection(ctx context.Context, projectID string) (ProjectConnectionInfo, error) {
	var response struct {
		Connections ProjectConnectionInfo `json:"connections"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/connections", nil, &response); err != nil {
		return ProjectConnectionInfo{}, err
	}
	return response.Connections, nil
}

func (c *Client) GetProjectObservability(ctx context.Context, projectID string) (ProjectObservability, error) {
	var response struct {
		Observability ProjectObservability `json:"observability"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/observability", nil, &response); err != nil {
		return ProjectObservability{}, err
	}
	return response.Observability, nil
}

func (c *Client) ListBackups(ctx context.Context, projectID string) ([]Backup, error) {
	var response struct {
		Backups []Backup `json:"backups"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/backups", nil, &response); err != nil {
		return nil, err
	}
	return response.Backups, nil
}

func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	var response struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/clusters", nil, &response); err != nil {
		return nil, err
	}
	return response.Clusters, nil
}

func (c *Client) ListPreviewDatabases(ctx context.Context, projectID string) ([]PreviewDetails, error) {
	var response struct {
		PreviewDatabases []Preview `json:"preview_databases"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/preview-databases", nil, &response); err != nil {
		return nil, err
	}

	details := make([]PreviewDetails, 0, len(response.PreviewDatabases))
	for _, preview := range response.PreviewDatabases {
		details = append(details, PreviewDetails{Preview: preview})
	}
	return details, nil
}

func (c *Client) GetPreviewConnection(ctx context.Context, previewID string) (PreviewConnectionInfo, error) {
	var response struct {
		Connections PreviewConnectionInfo `json:"connections"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/preview-databases/"+previewID+"/connections", nil, &response); err != nil {
		return PreviewConnectionInfo{}, err
	}
	return response.Connections, nil
}

func (c *Client) ListProjects(ctx context.Context, organizationID string) ([]Project, error) {
	path := "/v1/projects"
	if trimmed := strings.TrimSpace(organizationID); trimmed != "" {
		values := url.Values{}
		values.Set("organization_id", trimmed)
		path += "?" + values.Encode()
	}

	var response struct {
		Projects []Project `json:"projects"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return response.Projects, nil
}

func (c *Client) ResetPreviewDatabase(ctx context.Context, previewID string) (Job, error) {
	var response struct {
		Job Job `json:"job"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/preview-databases/"+previewID+"/reset", nil, &response); err != nil {
		return Job{}, err
	}
	return response.Job, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload any, dest any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &payload)
		return &APIError{
			Message:    payload.Error,
			StatusCode: response.StatusCode,
		}
	}

	if dest == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
