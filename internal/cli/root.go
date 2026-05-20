package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/config"
	"github.com/capy-base/capydb/cli/internal/envfile"
	"github.com/capy-base/capydb/cli/internal/gitignore"
	"github.com/capy-base/capydb/cli/internal/project"
)

type app struct {
	apiKey string
	apiURL string
	appURL string
	cwd    string
}

type resolvedAuth struct {
	APIKey           string
	APIURL           string
	AppURL           string
	OrganizationID   string
	OrganizationName string
	OrganizationSlug string
	Persist          bool
}

func Execute(version string) error {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}

	application := &app{cwd: workingDirectory}
	return newRootCommand(application, version).Execute()
}

func newRootCommand(application *app, version string) *cobra.Command {
	if strings.TrimSpace(version) == "" {
		version = "dev (built from source)"
	}

	root := &cobra.Command{
		Use:     "capydb",
		Short:   "CapyDB Postgres project CLI",
		Long:    "CapyDB links local projects to hosted Postgres databases, writes the right env vars, and handles repeatable project workflows.",
		Version: version,
	}

	root.PersistentFlags().StringVar(&application.apiURL, "api-url", "", "CapyDB API base URL")
	root.PersistentFlags().StringVar(&application.apiKey, "api-key", "", "CapyDB organization API key")
	root.PersistentFlags().StringVar(&application.appURL, "app-url", "", "CapyDB app URL for browser-based login and dashboard links")

	root.AddCommand(application.newLoginCommand())
	root.AddCommand(application.newLogoutCommand())
	root.AddCommand(application.newWhoamiCommand())
	root.AddCommand(application.newStatusCommand())
	root.AddCommand(application.newAuthCommand())
	root.AddCommand(application.newCreateCommand())
	root.AddCommand(application.newLinkCommand())
	root.AddCommand(application.newUnlinkCommand())
	root.AddCommand(application.newEnvCommand())
	root.AddCommand(application.newPreviewCommand())
	root.AddCommand(application.newBackupsCommand())
	root.AddCommand(application.newImportCommand())
	root.AddCommand(application.newRestoreCommand())
	root.AddCommand(application.newJobsCommand())
	root.AddCommand(application.newStudioCommand())
	root.AddCommand(application.newIntegrationsCommand())
	return root
}

func (a *app) newAuthCommand() *cobra.Command {
	authCommand := &cobra.Command{
		Use:   "auth",
		Short: "Manage CLI authentication",
	}

	authCommand.AddCommand(a.newLoginCommandForAuthGroup())
	authCommand.AddCommand(a.newLogoutCommandForAuthGroup())
	authCommand.AddCommand(a.newWhoamiCommandForAuthGroup())
	return authCommand
}

type loginOptions struct {
	deviceName string
	expiresIn  string
	name       string
	noOpen     bool
}

func (a *app) newLoginCommand() *cobra.Command {
	var options loginOptions

	command := &cobra.Command{
		Use:   "login",
		Short: "Log in to CapyDB from the browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogin(cmd, options)
		},
	}

	command.Flags().BoolVar(&options.noOpen, "no-open", false, "Print the authorization URL without opening a browser")
	command.Flags().StringVar(&options.name, "name", "", "Human-readable name for the CLI key")
	command.Flags().StringVar(&options.deviceName, "device-name", "", "Device label stored with the CLI key")
	command.Flags().StringVar(&options.expiresIn, "expires-in", "", "Optional key lifetime such as 24h, 7d, 30d, or an RFC3339 timestamp")
	return command
}

func (a *app) newLoginCommandForAuthGroup() *cobra.Command {
	var options loginOptions

	command := &cobra.Command{
		Use:   "login",
		Short: "Log in to CapyDB from the browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogin(cmd, options)
		},
	}

	command.Flags().BoolVar(&options.noOpen, "no-open", false, "Print the authorization URL without opening a browser")
	command.Flags().StringVar(&options.name, "name", "", "Human-readable name for the CLI key")
	command.Flags().StringVar(&options.deviceName, "device-name", "", "Device label stored with the CLI key")
	command.Flags().StringVar(&options.expiresIn, "expires-in", "", "Optional key lifetime such as 24h, 7d, 30d, or an RFC3339 timestamp")
	return command
}

func (a *app) runLogin(cmd *cobra.Command, options loginOptions) error {
	ctx := cmd.Context()
	apiURL := a.resolveAPIURL("")
	appURL := a.resolveAppURL(apiURL)

	if manualAPIKey := firstNonEmpty(a.apiKey, os.Getenv("CAPYDB_API_KEY")); strings.TrimSpace(manualAPIKey) != "" {
		client := api.NewClient(apiURL, manualAPIKey)
		viewer, err := client.GetViewerResponse(ctx)
		if err != nil {
			return fmt.Errorf("validate api key: %w", err)
		}

		authConfig := resolvedAuth{
			APIKey:  strings.TrimSpace(manualAPIKey),
			APIURL:  apiURL,
			AppURL:  appURL,
			Persist: true,
		}
		if viewer.Organization != nil {
			authConfig.OrganizationID = viewer.Organization.ID
			authConfig.OrganizationName = viewer.Organization.Name
			authConfig.OrganizationSlug = viewer.Organization.Slug
		}
		return a.saveAuthAndPrint(cmd, authConfig)
	}

	anonymousClient := api.NewClient(apiURL, "")
	deviceName := resolveCLILoginDeviceName(options.deviceName)
	expiresAt, err := resolveCLILoginExpiry(options.expiresIn)
	if err != nil {
		return err
	}
	session, err := anonymousClient.StartCLILoginSession(ctx, api.CLILoginSessionStartRequest{
		DeviceName: deviceName,
		ExpiresAt:  expiresAt,
		Name:       resolveCLILoginName(options.name, deviceName),
	})
	if err != nil {
		return fmt.Errorf("start login session: %w", err)
	}

	loginURL := strings.TrimRight(appURL, "/") + "/dashboard/cli/login?session=" + url.QueryEscape(session.SessionID)
	fmt.Fprintf(cmd.OutOrStdout(), "Open this URL to authorize the CLI:\n%s\n", loginURL)

	if !options.noOpen {
		if err := openURL(loginURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Could not open browser automatically: %v\n", err)
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Waiting for browser authorization...")
	status, err := waitForCLILoginSession(ctx, anonymousClient, session.SessionID, session.PollToken, session.ExpiresAt)
	if err != nil {
		return err
	}

	authConfig := resolvedAuth{
		APIKey:           status.PlaintextAPIKey,
		APIURL:           apiURL,
		AppURL:           appURL,
		OrganizationID:   status.OrganizationID,
		OrganizationName: status.OrganizationName,
		OrganizationSlug: status.OrganizationSlug,
		Persist:          true,
	}
	return a.saveAuthAndPrint(cmd, authConfig)
}

func (a *app) newLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear saved CLI authentication",
		RunE:  a.runLogout,
	}
}

func (a *app) newLogoutCommandForAuthGroup() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear saved CLI authentication",
		RunE:  a.runLogout,
	}
}

func (a *app) runLogout(cmd *cobra.Command, args []string) error {
	path, err := config.UserConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(cmd.OutOrStdout(), "No saved CLI session found.")
			return nil
		}
		return fmt.Errorf("remove saved auth: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Cleared saved CLI session.")
	return nil
}

func (a *app) newWhoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current CapyDB CLI identity",
		RunE:  a.runWhoami,
	}
}

func (a *app) newWhoamiCommandForAuthGroup() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current CapyDB CLI identity",
		RunE:  a.runWhoami,
	}
}

func (a *app) runWhoami(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, authConfig, err := a.resolveClient(ctx, false)
	if err != nil {
		return err
	}

	viewer, err := client.GetViewerResponse(ctx)
	if err != nil {
		return fmt.Errorf("load viewer: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "api_url: %s\n", authConfig.APIURL)
	fmt.Fprintf(cmd.OutOrStdout(), "app_url: %s\n", authConfig.AppURL)
	fmt.Fprintf(cmd.OutOrStdout(), "auth_source: %s\n", firstNonEmpty(viewer.Principal.AuthSource, "api_key"))
	if viewer.Organization != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "organization_id: %s\n", viewer.Organization.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "organization_name: %s\n", viewer.Organization.Name)
		fmt.Fprintf(cmd.OutOrStdout(), "organization_slug: %s\n", viewer.Organization.Slug)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "organization_id: -\n")
	}
	return nil
}

func (a *app) newStatusCommand() *cobra.Command {
	var options statusOptions

	command := &cobra.Command{
		Use:   "status",
		Short: "Show saved auth, local project link, and optional remote project status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runStatus(cmd, options)
		},
	}

	command.Flags().StringVar(&options.projectRef, "project", "", "Project id, slug, or name for remote status")
	command.Flags().BoolVar(&options.remote, "remote", false, "Check the CapyDB API and linked project")
	return command
}

func (a *app) newCreateCommand() *cobra.Command {
	var clusterRef string
	var envFileOverride string
	var nonInteractive bool
	var projectName string
	var region string
	var slug string

	command := &cobra.Command{
		Use:     "create",
		Aliases: []string{"init"},
		Short:   "Create a CapyDB project and link the current directory to it",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			detection, err := a.detectProject(envFileOverride)
			if err != nil {
				return err
			}

			authConfig, err := a.resolveAuth(true)
			if err != nil {
				return err
			}

			client := api.NewClient(authConfig.APIURL, authConfig.APIKey)
			viewer, err := client.GetViewer(ctx)
			if err != nil {
				return fmt.Errorf("load organization billing: %w", err)
			}
			if err := ensureViewerCanProvision(viewer); err != nil {
				return err
			}
			clusters, err := client.ListClusters(ctx)
			if err != nil {
				return fmt.Errorf("list clusters: %w", err)
			}
			if err := a.persistResolvedAuth(authConfig); err != nil {
				return err
			}

			cluster, err := selectCluster(clusters, clusterRef, nonInteractive)
			if err != nil {
				return err
			}

			request := api.CreateProjectRequest{
				ClusterID: cluster.ID,
				Name:      firstNonEmpty(projectName, detection.ProjectName),
				Region:    strings.TrimSpace(region),
				Slug:      strings.TrimSpace(slug),
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Creating project %s on cluster %s (%s)\n", request.Name, cluster.Name, cluster.Region)
			createdProject, job, err := client.CreateProject(ctx, request)
			if err != nil {
				return fmt.Errorf("create project: %w", err)
			}

			if job.ID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Waiting for provision job %s\n", job.ID)
				if job, err = waitForJob(ctx, client, job.ID); err != nil {
					return err
				}
				if job.State != "completed" {
					return fmt.Errorf("project provisioning ended in %s: %s", job.State, job.Error)
				}
			}

			linkConfig := config.ProjectConfig{
				AppPath:       detection.AppPath,
				APIURL:        authConfig.APIURL,
				ClusterID:     cluster.ID,
				DatabaseLayer: detection.DatabaseLayer,
				EnvFile:       detection.EnvFile,
				Framework:     detection.Framework,
				Profile:       detection.Profile,
				ProjectID:     createdProject.ID,
				ProjectName:   createdProject.Name,
				ProjectSlug:   createdProject.Slug,
			}

			if err := a.writeProjectEnv(ctx, client, createdProject.ID, linkConfig, envFileOverride); err != nil {
				return err
			}

			printLinkSummary(cmd, detection, linkConfig)
			return nil
		},
	}

	command.Flags().StringVar(&clusterRef, "cluster", "", "Cluster id or name")
	command.Flags().StringVar(&envFileOverride, "env-file", "", "Target env file")
	command.Flags().BoolVar(&nonInteractive, "yes", false, "Use the first cluster when no cluster is specified")
	command.Flags().StringVar(&projectName, "name", "", "Project name")
	command.Flags().StringVar(&region, "region", "", "Region override")
	command.Flags().StringVar(&slug, "slug", "", "Project slug override")
	return command
}

func (a *app) newLinkCommand() *cobra.Command {
	var envFileOverride string
	var projectRef string

	command := &cobra.Command{
		Use:     "link",
		Aliases: []string{"connect"},
		Short:   "Link the current directory to an existing CapyDB project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			detection, err := a.detectProject(envFileOverride)
			if err != nil {
				return err
			}

			authConfig, err := a.resolveAuth(true)
			if err != nil {
				return err
			}

			client := api.NewClient(authConfig.APIURL, authConfig.APIKey)
			resolvedProject, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			linkConfig := config.ProjectConfig{
				AppPath:       detection.AppPath,
				APIURL:        authConfig.APIURL,
				ClusterID:     resolvedProject.ClusterID,
				DatabaseLayer: detection.DatabaseLayer,
				EnvFile:       detection.EnvFile,
				Framework:     detection.Framework,
				Profile:       detection.Profile,
				ProjectID:     resolvedProject.ID,
				ProjectName:   resolvedProject.Name,
				ProjectSlug:   resolvedProject.Slug,
			}

			if err := a.writeProjectEnv(ctx, client, resolvedProject.ID, linkConfig, envFileOverride); err != nil {
				return err
			}
			if err := a.persistResolvedAuth(authConfig); err != nil {
				return err
			}

			printLinkSummary(cmd, detection, linkConfig)
			return nil
		},
	}

	command.Flags().StringVar(&envFileOverride, "env-file", "", "Target env file")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func (a *app) newUnlinkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink",
		Short: "Remove the local CapyDB project link",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.ProjectConfigPath(a.cwd)
			if err := os.Remove(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(cmd.OutOrStdout(), "No local project link found.")
					return nil
				}
				return fmt.Errorf("remove local project link: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Removed local project link.")
			return nil
		},
	}
}

func (a *app) newEnvCommand() *cobra.Command {
	var envFileOverride string

	envCommand := &cobra.Command{
		Use:   "env",
		Short: "Manage local env files for the linked CapyDB project",
	}

	pullCommand := &cobra.Command{
		Use:   "pull",
		Short: "Refresh local env vars from the linked CapyDB project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			linkConfig, err := config.LoadProjectConfig(a.cwd)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("no local project link found; run `capydb link` or `capydb create` first")
				}
				return err
			}

			authConfig, err := a.resolveAuth(true, linkConfig.APIURL)
			if err != nil {
				return err
			}

			client := api.NewClient(authConfig.APIURL, authConfig.APIKey)
			if err := a.writeProjectEnv(ctx, client, linkConfig.ProjectID, linkConfig, envFileOverride); err != nil {
				return err
			}
			if err := a.persistResolvedAuth(authConfig); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s from project %s\n", firstNonEmpty(envFileOverride, linkConfig.EnvFile), linkConfig.ProjectID)
			for _, step := range project.BuildNextSteps(projectDetectionFromConfig(linkConfig)) {
				fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", step)
			}
			return nil
		},
	}

	pullCommand.Flags().StringVar(&envFileOverride, "env-file", "", "Override the target env file")
	envCommand.AddCommand(pullCommand)
	return envCommand
}

func (a *app) detectProject(envFileOverride string) (project.Detection, error) {
	detection, err := project.Detect(a.cwd, envFileOverride)
	if err == nil {
		return detection, nil
	}

	var ambiguous *project.AmbiguousProjectError
	if !errors.As(err, &ambiguous) {
		return project.Detection{}, err
	}
	if !stdinIsInteractive() {
		return project.Detection{}, err
	}

	return selectAppCandidate(ambiguous.Candidates)
}

func (a *app) resolveAuth(prompt bool, fallbackAPIURL ...string) (resolvedAuth, error) {
	apiURL := a.resolveAPIURL(firstNonEmpty(fallbackAPIURL...))
	appURL := a.resolveAppURL(apiURL)

	if trimmed := strings.TrimSpace(a.apiKey); trimmed != "" {
		return resolvedAuth{APIKey: trimmed, APIURL: apiURL, AppURL: appURL}, nil
	}
	if trimmed := strings.TrimSpace(os.Getenv("CAPYDB_API_KEY")); trimmed != "" {
		return resolvedAuth{APIKey: trimmed, APIURL: apiURL, AppURL: appURL}, nil
	}

	userConfig, err := config.LoadUserConfig()
	if err == nil && strings.TrimSpace(userConfig.APIKey) != "" {
		return resolvedAuth{
			APIKey:           strings.TrimSpace(userConfig.APIKey),
			APIURL:           a.resolveAPIURL(firstNonEmpty(apiURL, userConfig.APIURL)),
			AppURL:           a.resolveAppURL(firstNonEmpty(appURL, userConfig.AppURL)),
			OrganizationID:   userConfig.OrganizationID,
			OrganizationName: userConfig.OrganizationName,
			OrganizationSlug: userConfig.OrganizationSlug,
		}, nil
	}

	if !prompt || !stdinIsInteractive() {
		return resolvedAuth{}, fmt.Errorf("no api key available; pass --api-key, set CAPYDB_API_KEY, or run `capydb login`")
	}

	value, err := promptLine("CapyDB API key")
	if err != nil {
		return resolvedAuth{}, err
	}
	if strings.TrimSpace(value) == "" {
		return resolvedAuth{}, fmt.Errorf("api key cannot be empty")
	}
	return resolvedAuth{
		APIKey:  strings.TrimSpace(value),
		APIURL:  apiURL,
		AppURL:  appURL,
		Persist: true,
	}, nil
}

func (a *app) resolveAPIURL(fallback string) string {
	if trimmed := strings.TrimSpace(a.apiURL); trimmed != "" {
		return strings.TrimRight(trimmed, "/")
	}
	if trimmed := strings.TrimSpace(os.Getenv("CAPYDB_API_URL")); trimmed != "" {
		return strings.TrimRight(trimmed, "/")
	}
	if trimmed := strings.TrimSpace(fallback); trimmed != "" {
		return strings.TrimRight(trimmed, "/")
	}

	userConfig, err := config.LoadUserConfig()
	if err == nil && strings.TrimSpace(userConfig.APIURL) != "" {
		return strings.TrimRight(strings.TrimSpace(userConfig.APIURL), "/")
	}
	return config.DefaultAPIURL()
}

func (a *app) persistResolvedAuth(authConfig resolvedAuth) error {
	if !authConfig.Persist {
		return nil
	}

	return config.SaveUserConfig(config.UserConfig{
		APIKey:           authConfig.APIKey,
		APIURL:           authConfig.APIURL,
		AppURL:           authConfig.AppURL,
		OrganizationID:   authConfig.OrganizationID,
		OrganizationName: authConfig.OrganizationName,
		OrganizationSlug: authConfig.OrganizationSlug,
	})
}

func (a *app) saveAuthAndPrint(cmd *cobra.Command, authConfig resolvedAuth) error {
	if err := a.persistResolvedAuth(authConfig); err != nil {
		return err
	}

	path, err := config.UserConfigPath()
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Saved CLI auth to %s\n", path)
	fmt.Fprintf(cmd.OutOrStdout(), "API URL: %s\n", authConfig.APIURL)
	if strings.TrimSpace(authConfig.OrganizationName) != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Organization: %s (%s)\n", authConfig.OrganizationName, firstNonEmpty(authConfig.OrganizationSlug, authConfig.OrganizationID))
	}
	return nil
}

func (a *app) writeProjectEnv(ctx context.Context, client *api.Client, projectID string, linkConfig config.ProjectConfig, envFileOverride string) error {
	projectDetails, _, err := client.GetProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("fetch project: %w", err)
	}
	connections, err := client.GetProjectConnection(ctx, projectID)
	if err != nil {
		return fmt.Errorf("fetch project connections: %w", err)
	}

	envPath := linkConfig.EnvFile
	if trimmed := strings.TrimSpace(envFileOverride); trimmed != "" {
		envPath = trimmed
	}

	detection := projectDetectionFromConfig(linkConfig)
	plan := project.BuildEnvPlan(detection, connections.DirectURL, connections.PooledURL)
	if err := envfile.Upsert(envTargetPath(a.cwd, linkConfig.AppPath, envPath), plan.Vars); err != nil {
		return err
	}

	linkConfig.ClusterID = firstNonEmpty(linkConfig.ClusterID, projectDetails.ClusterID)
	linkConfig.DatabaseURLVar = plan.DatabaseURLVar
	linkConfig.DirectURLVar = plan.DirectURLVar
	linkConfig.PooledURLVar = plan.PooledURLVar
	linkConfig.EnvFile = envPath
	linkConfig.OrganizationID = projectDetails.OrganizationID
	linkConfig.ProjectID = projectDetails.ID
	linkConfig.ProjectName = projectDetails.Name
	linkConfig.ProjectSlug = projectDetails.Slug

	if err := config.SaveProjectConfig(a.cwd, linkConfig); err != nil {
		return err
	}
	if err := gitignore.EnsureLocalConfigIgnored(a.cwd); err != nil {
		return err
	}
	return nil
}

func resolveCLILoginDeviceName(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	hostname, err := os.Hostname()
	if err == nil && strings.TrimSpace(hostname) != "" {
		return strings.TrimSpace(hostname)
	}
	return "local-" + runtime.GOOS
}

func resolveCLILoginName(value, deviceName string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	if strings.TrimSpace(deviceName) == "" {
		return "CapyDB CLI"
	}
	return "CLI on " + strings.TrimSpace(deviceName)
}

func resolveCLILoginExpiry(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		value := parsed.UTC()
		if !value.After(time.Now().UTC()) {
			return nil, fmt.Errorf("invalid --expires-in value %q; expiration must be in the future", trimmed)
		}
		return &value, nil
	}

	duration, err := parseCLILoginDuration(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid --expires-in value %q; use 24h, 7d, 30d, or an RFC3339 timestamp", trimmed)
	}
	value := time.Now().UTC().Add(duration)
	return &value, nil
}

func parseCLILoginDuration(raw string) (time.Duration, error) {
	if duration, err := time.ParseDuration(raw); err == nil {
		if duration <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return duration, nil
	}

	if len(raw) < 2 {
		return 0, fmt.Errorf("unsupported duration")
	}

	unit := raw[len(raw)-1]
	amount, err := strconv.Atoi(raw[:len(raw)-1])
	if err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}

	switch unit {
	case 'd', 'D':
		return time.Duration(amount) * 24 * time.Hour, nil
	case 'w', 'W':
		return time.Duration(amount) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported duration unit")
	}
}

func waitForCLILoginSession(ctx context.Context, client *api.Client, sessionID, pollToken string, expiresAt time.Time) (api.CLILoginSessionStatus, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		status, err := client.PollCLILoginSession(ctx, sessionID, pollToken)
		if err == nil {
			if status.State == "completed" && strings.TrimSpace(status.PlaintextAPIKey) != "" {
				return status, nil
			}
		} else {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 410 {
				return api.CLILoginSessionStatus{}, fmt.Errorf("login session expired; run `capydb login` again")
			}
			if errors.As(err, &apiErr) && apiErr.StatusCode != 404 && apiErr.StatusCode != 401 {
				return api.CLILoginSessionStatus{}, fmt.Errorf("poll login session: %w", err)
			}
		}

		if !expiresAt.IsZero() && time.Now().UTC().After(expiresAt) {
			return api.CLILoginSessionStatus{}, fmt.Errorf("login session expired; run `capydb login` again")
		}

		select {
		case <-ctx.Done():
			return api.CLILoginSessionStatus{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func envTargetPath(cwd, appPath, envPath string) string {
	envPath = strings.TrimSpace(envPath)
	if filepath.IsAbs(envPath) {
		return envPath
	}
	if strings.Contains(envPath, string(filepath.Separator)) {
		return filepath.Join(cwd, envPath)
	}
	base := cwd
	if trimmed := strings.TrimSpace(appPath); trimmed != "" {
		base = filepath.Join(cwd, trimmed)
	}
	return filepath.Join(base, envPath)
}

func projectDetectionFromConfig(cfg config.ProjectConfig) project.Detection {
	return project.Detection{
		AppPath:       cfg.AppPath,
		DatabaseLayer: cfg.DatabaseLayer,
		EnvFile:       cfg.EnvFile,
		Framework:     cfg.Framework,
		Profile:       cfg.Profile,
		ProjectName:   cfg.ProjectName,
	}
}

func printLinkSummary(cmd *cobra.Command, detection project.Detection, linkConfig config.ProjectConfig) {
	fmt.Fprintf(cmd.OutOrStdout(), "Linked %s to project %s\n", firstNonEmpty(detection.ProjectName, filepath.Base(linkConfig.AppPath), filepath.Base(linkConfig.ProjectName)), linkConfig.ProjectID)
	fmt.Fprintf(cmd.OutOrStdout(), "App path: %s\n", firstNonEmpty(detection.AppPath, "."))
	fmt.Fprintf(cmd.OutOrStdout(), "Env file updated: %s\n", firstNonEmpty(filepath.Join(detection.AppPath, detection.EnvFile), detection.EnvFile))
	fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\n", firstNonEmpty(detection.Profile, detection.Framework))
	for _, step := range project.BuildNextSteps(detection) {
		fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", step)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ensureViewerCanProvision(viewer api.Viewer) error {
	if viewer.Organization == nil {
		return fmt.Errorf("select an organization before creating a project")
	}

	if billingAllowsProvisioning(viewer.Organization.BillingPlan, viewer.Organization.BillingStatus, viewer.Organization.BillingPeriodEnd) {
		return nil
	}

	return fmt.Errorf(
		"organization billing is %s on plan %s; open the dashboard billing page before creating a project",
		viewer.Organization.BillingStatus,
		viewer.Organization.BillingPlan,
	)
}

func billingAllowsProvisioning(plan, status string, periodEnd *time.Time) bool {
	if plan == "" || plan == "none" {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "trialing":
		return true
	case "past_due", "canceled":
		return periodEnd != nil && periodEnd.After(time.Now().UTC())
	default:
		return false
	}
}

func promptLine(label string) (string, error) {
	fmt.Printf("%s: ", label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) {
		if errors.Is(err, context.Canceled) {
			return "", err
		}
	}
	return strings.TrimSpace(line), nil
}

func stdinIsInteractive() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true") {
		return false
	}

	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func selectCluster(clusters []api.Cluster, ref string, nonInteractive bool) (api.Cluster, error) {
	if len(clusters) == 0 {
		return api.Cluster{}, fmt.Errorf("no clusters available for this api key")
	}

	if trimmed := strings.TrimSpace(ref); trimmed != "" {
		for _, cluster := range clusters {
			if cluster.ID == trimmed || strings.EqualFold(cluster.Name, trimmed) {
				return cluster, nil
			}
		}
		return api.Cluster{}, fmt.Errorf("cluster %q not found", trimmed)
	}

	if len(clusters) == 1 || nonInteractive {
		return clusters[0], nil
	}

	fmt.Println("Select a cluster:")
	for index, cluster := range clusters {
		fmt.Printf("  %d. %s (%s, %s)\n", index+1, cluster.Name, cluster.Region, cluster.ID)
	}

	value, err := promptLine("Cluster number")
	if err != nil {
		return api.Cluster{}, err
	}
	selection, err := strconv.Atoi(value)
	if err != nil || selection < 1 || selection > len(clusters) {
		return api.Cluster{}, fmt.Errorf("invalid cluster selection")
	}

	return clusters[selection-1], nil
}

func selectAppCandidate(candidates []project.Detection) (project.Detection, error) {
	if len(candidates) == 0 {
		return project.Detection{}, fmt.Errorf("no app candidates found")
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	fmt.Println("Select the local app to link:")
	for index, candidate := range candidates {
		fmt.Printf(
			"  %d. %s [%s]\n",
			index+1,
			firstNonEmpty(candidate.AppPath, "."),
			firstNonEmpty(candidate.Profile, candidate.Framework),
		)
	}

	value, err := promptLine("App number")
	if err != nil {
		return project.Detection{}, err
	}
	selection, err := strconv.Atoi(value)
	if err != nil || selection < 1 || selection > len(candidates) {
		return project.Detection{}, fmt.Errorf("invalid app selection")
	}
	return candidates[selection-1], nil
}

func waitForJob(ctx context.Context, client *api.Client, jobID string) (api.Job, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		job, err := client.GetJob(ctx, jobID)
		if err != nil {
			return api.Job{}, fmt.Errorf("poll job %s: %w", jobID, err)
		}

		switch job.State {
		case "completed", "failed":
			return job, nil
		}

		select {
		case <-ctx.Done():
			return api.Job{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
