package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/config"
	projectdetect "github.com/capy-base/capydb/cli/internal/project"
)

type integrationEnvPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type vercelEnvPayload struct {
	Comment   string   `json:"comment,omitempty"`
	GitBranch string   `json:"gitBranch,omitempty"`
	Key       string   `json:"key"`
	Target    []string `json:"target"`
	Type      string   `json:"type"`
	Value     string   `json:"value"`
}

type netlifyEnvValue struct {
	Context          string `json:"context"`
	ContextParameter string `json:"context_parameter,omitempty"`
	Value            string `json:"value"`
}

type netlifyEnvPayload struct {
	IsSecret bool              `json:"is_secret"`
	Key      string            `json:"key"`
	Scopes   []string          `json:"scopes"`
	Values   []netlifyEnvValue `json:"values"`
}

func (a *app) newIntegrationsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "integrations",
		Short: "Print integration payloads for a linked CapyDB project",
	}

	var branch string
	var netlifyContext string
	var projectRef string
	var target string
	envCommand := &cobra.Command{
		Use:   "env",
		Short: "Print environment variable payloads for deployment platforms",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			resolvedProject, plan, err := a.resolveIntegrationEnvPlan(ctx, client, projectRef)
			if err != nil {
				return err
			}

			switch strings.ToLower(strings.TrimSpace(target)) {
			case "", "dotenv":
				writeDotenvPayload(cmd, plan.Vars)
			case "json":
				writeJSONPayload(cmd, buildIntegrationEnvPayload(plan.Vars))
			case "vercel":
				writeJSONPayload(cmd, buildVercelEnvPayload(plan.Vars, branch))
			case "netlify":
				writeJSONPayload(cmd, buildNetlifyEnvPayload(plan.Vars, netlifyContext))
			default:
				return fmt.Errorf("--target must be dotenv, json, vercel, or netlify")
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "project: %s (%s)\n", resolvedProject.Name, resolvedProject.ID)
			return nil
		},
	}

	envCommand.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	envCommand.Flags().StringVar(&target, "target", "dotenv", "Output target: dotenv, json, vercel, or netlify")
	envCommand.Flags().StringVar(&branch, "branch", "", "Vercel preview branch for branch-scoped env vars")
	envCommand.Flags().StringVar(&netlifyContext, "netlify-context", "all", "Netlify context: all, dev, branch-deploy, deploy-preview, production, or branch")

	command.AddCommand(envCommand)
	return command
}

func (a *app) resolveIntegrationEnvPlan(ctx context.Context, client *api.Client, projectRef string) (api.Project, projectdetect.EnvPlan, error) {
	detection, err := a.integrationDetection()
	if err != nil {
		return api.Project{}, projectdetect.EnvPlan{}, err
	}

	resolvedProject, err := a.resolveProject(ctx, client, projectRef)
	if err != nil {
		return api.Project{}, projectdetect.EnvPlan{}, err
	}

	connections, err := client.GetProjectConnection(ctx, resolvedProject.ID)
	if err != nil {
		return api.Project{}, projectdetect.EnvPlan{}, fmt.Errorf("fetch project connections: %w", err)
	}

	return resolvedProject, projectdetect.BuildEnvPlan(detection, connections.DirectURL, connections.PooledURL), nil
}

func (a *app) integrationDetection() (projectdetect.Detection, error) {
	linkConfig, err := config.LoadProjectConfig(a.cwd)
	if err == nil {
		return projectDetectionFromConfig(linkConfig), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return projectdetect.Detection{}, err
	}

	detection, err := a.detectProject("")
	if err != nil {
		return projectdetect.Detection{}, err
	}
	return detection, nil
}

func buildIntegrationEnvPayload(vars map[string]string) []integrationEnvPayload {
	keys := sortedEnvKeys(vars)
	payload := make([]integrationEnvPayload, 0, len(keys))
	for _, key := range keys {
		payload = append(payload, integrationEnvPayload{Key: key, Value: vars[key]})
	}
	return payload
}

func buildVercelEnvPayload(vars map[string]string, branch string) []vercelEnvPayload {
	keys := sortedEnvKeys(vars)
	targets := []string{"preview", "production"}
	if strings.TrimSpace(branch) != "" {
		targets = []string{"preview"}
	}

	payload := make([]vercelEnvPayload, 0, len(keys))
	for _, key := range keys {
		item := vercelEnvPayload{
			Comment: "CapyDB Postgres connection string",
			Key:     key,
			Target:  targets,
			Type:    "sensitive",
			Value:   vars[key],
		}
		if strings.TrimSpace(branch) != "" {
			item.GitBranch = strings.TrimSpace(branch)
		}
		payload = append(payload, item)
	}
	return payload
}

func buildNetlifyEnvPayload(vars map[string]string, contextName string) []netlifyEnvPayload {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		contextName = "all"
	}

	keys := sortedEnvKeys(vars)
	payload := make([]netlifyEnvPayload, 0, len(keys))
	for _, key := range keys {
		payload = append(payload, netlifyEnvPayload{
			IsSecret: true,
			Key:      key,
			Scopes:   []string{"builds", "functions"},
			Values: []netlifyEnvValue{{
				Context: contextName,
				Value:   vars[key],
			}},
		})
	}
	return payload
}

func sortedEnvKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for key, value := range vars {
		if strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeDotenvPayload(cmd *cobra.Command, vars map[string]string) {
	for _, key := range sortedEnvKeys(vars) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\n", key, quoteEnvValue(vars[key]))
	}
}

func writeJSONPayload(cmd *cobra.Command, payload any) {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
}

func quoteEnvValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
