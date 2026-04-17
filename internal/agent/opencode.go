// Package agent provides abstractions for AI agent interactions.
// This file implements the OpenCode adapter using the official Go SDK.
//
// References:
// - OpenCode Server API: https://open-code.ai/docs/en/server
// - OpenCode SDK: https://opencode.ai/docs/sdk
package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"

	"github.com/openagent/github-bridge/internal/config"
)

const defaultOpenCodeTimeout = 30 * time.Second

// OpenCodeAdapter implements the Agent interface using the official OpenCode Go SDK.
//
// It dispatches tasks to OpenCode and returns immediately (fire-and-forget).
// OpenCode is responsible for:
// - Executing the task in an isolated git worktree
// - Creating PRs or posting comments to GitHub
// - Managing its own GitHub authentication
//
// API Reference: https://opencode.ai/docs/sdk
type OpenCodeAdapter struct {
	client          *opencode.Client
	model           *OpenCodeModel
	worktreeManager *WorktreeManagerClient
}

// OpenCodeModel represents the configured model used for SDK requests.
// Reference: https://opencode.ai/docs/sdk
type OpenCodeModel struct {
	ProviderID string
	ModelID    string
}

// openCodeHealthResponse represents the global health response.
// Reference: https://open-code.ai/docs/en/server#global
type openCodeHealthResponse struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

// NewOpenCodeAdapter creates a new OpenCode adapter.
//
// Authentication:
// If password is set, HTTP Basic Auth is applied through SDK request options.
// Reference: https://open-code.ai/docs/en/server#authentication
func NewOpenCodeAdapter(cfg config.OpenCodeConfig) *OpenCodeAdapter {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultOpenCodeTimeout
	}

	clientOptions := []option.RequestOption{
		option.WithBaseURL(strings.TrimSuffix(cfg.Host, "/")),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
	}

	if authHeader := buildBasicAuthHeader(cfg.Username, cfg.Password, "opencode"); authHeader != "" {
		clientOptions = append(clientOptions, option.WithHeader("Authorization", authHeader))
	}

	return &OpenCodeAdapter{
		client:          opencode.NewClient(clientOptions...),
		model:           parseOpenCodeModel(cfg.DefaultModel),
		worktreeManager: NewWorktreeManagerClient(cfg, timeout),
	}
}

// DispatchTask sends a task to OpenCode and returns immediately.
//
// Flow:
// 1. Reuse an existing session OR create a new isolated session for this issue/PR
// 2. If new session: call the companion worktree-manager service, then create an OpenCode session in that path
// 3. Send a prompt with no reply so dispatch remains fire-and-forget
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, task TaskContext) (*DispatchResult, error) {
	sessionID := task.AgentSessionID
	worktreePath := ""

	if sessionID != "" && isPRScopedTask(task) {
		if _, err := a.ensureWorktree(ctx, task); err != nil {
			return &DispatchResult{
				Dispatched: false,
				TaskID:     sessionID,
				Error:      fmt.Sprintf("failed to refresh PR worktree: %v", err),
			}, err
		}
		fmt.Printf("[OpenCode] Reusing existing session: %s\n", sessionID)
	}

	if sessionID == "" {
		var err error
		worktreePath, err = a.ensureWorktree(ctx, task)
		if err != nil {
			return &DispatchResult{
				Dispatched: false,
				Error:      fmt.Sprintf("failed to prepare isolated worktree: %v", err),
			}, err
		}

		session, err := a.createSession(ctx, task.SessionKey, worktreePath)
		if err != nil {
			return &DispatchResult{
				Dispatched: false,
				Error:      fmt.Sprintf("failed to create isolated session: %v", err),
			}, err
		}
		sessionID = session.ID
	}

	if err := a.sendPromptAsync(ctx, sessionID, task.Prompt); err != nil {
		return &DispatchResult{
			Dispatched: false,
			TaskID:     sessionID,
			Error:      fmt.Sprintf("failed to send prompt: %v", err),
		}, err
	}

	return &DispatchResult{
		Dispatched: true,
		TaskID:     sessionID,
	}, nil
}

// HealthCheck verifies the OpenCode server and companion worktree service are reachable.
// Reference: https://open-code.ai/docs/en/server#global
func (a *OpenCodeAdapter) HealthCheck(ctx context.Context) error {
	return a.HealthStatus(ctx).Err()
}

// HealthStatus returns structured health details for the OpenCode server and worktree manager.
// Reference: https://open-code.ai/docs/en/server#global
func (a *OpenCodeAdapter) HealthStatus(ctx context.Context) HealthReport {
	repositoryStatus := a.repositoryHealthStatus(ctx)

	return HealthReport{
		Healthy: repositoryStatus.Healthy,
		Repositories: map[string]RepositoryHealthStatus{
			defaultHealthRepository: repositoryStatus,
		},
	}
}

func (a *OpenCodeAdapter) repositoryHealthStatus(ctx context.Context) RepositoryHealthStatus {
	openCodeStatus := a.openCodeHealthStatus(ctx)
	worktreeStatus := ServiceHealthStatus{
		Error: "worktree-manager client is not configured",
	}
	if a.worktreeManager != nil {
		worktreeStatus = a.worktreeManager.HealthStatus(ctx)
	}

	return RepositoryHealthStatus{
		Healthy:         openCodeStatus.Healthy && worktreeStatus.Healthy,
		OpenCode:        openCodeStatus,
		WorktreeManager: worktreeStatus,
	}
}

func (a *OpenCodeAdapter) openCodeHealthStatus(ctx context.Context) ServiceHealthStatus {
	var health openCodeHealthResponse
	if err := a.client.Get(ctx, "global/health", nil, &health); err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("health check request failed: %v", err),
		}
	}

	if !health.Healthy {
		return ServiceHealthStatus{
			Error:   "health check returned unhealthy response",
			Version: health.Version,
		}
	}

	return ServiceHealthStatus{
		Healthy: true,
		Version: health.Version,
	}
}

// createIsolatedSession prepares the worktree through the companion service
// and then creates the long-lived OpenCode session inside that directory.
func (a *OpenCodeAdapter) createIsolatedSession(ctx context.Context, task TaskContext) (*opencode.Session, error) {
	worktreePath, err := a.ensureWorktree(ctx, task)
	if err != nil {
		return nil, err
	}

	finalSession, err := a.createSession(ctx, task.SessionKey, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create final session in worktree %s: %w", worktreePath, err)
	}

	return finalSession, nil
}

func (a *OpenCodeAdapter) ensureWorktree(ctx context.Context, task TaskContext) (string, error) {
	if a.worktreeManager == nil || a.worktreeManager.baseURL == "" {
		return "", fmt.Errorf("worktree-manager host is not configured")
	}

	worktreeReq, err := buildWorktreeCreateRequest(task)
	if err != nil {
		return "", err
	}

	worktreeResult, err := a.worktreeManager.CreateOrReuse(ctx, worktreeReq)
	if err != nil {
		return "", fmt.Errorf("failed to create or reuse worktree: %w", err)
	}
	if strings.TrimSpace(worktreeResult.WorktreePath) == "" {
		return "", fmt.Errorf("worktree-manager returned an empty worktreePath")
	}

	return worktreeResult.WorktreePath, nil
}

// createSession creates a new session in OpenCode with the specified working directory.
// Reference: https://opencode.ai/docs/sdk
func (a *OpenCodeAdapter) createSession(ctx context.Context, title, directory string) (*opencode.Session, error) {
	session, err := a.client.Session.New(ctx, opencode.SessionNewParams{
		Title:     opencode.F(buildSessionTitle(title, time.Now())),
		Directory: opencode.F(directory),
	})
	if err != nil {
		return nil, fmt.Errorf("create session failed: %w", err)
	}

	return session, nil
}

func buildSessionTitle(title string, now time.Time) string {
	return fmt.Sprintf("%s-%s", title, now.Local().Format("20060102-150405"))
}

// sendPromptAsync sends a prompt to the session asynchronously (fire-and-forget).
// The Go SDK does not expose a typed prompt_async helper yet, so we dispatch the
// documented async server endpoint through the SDK client to retain shared base URL,
// auth, timeout, and retry behavior.
// References:
// - OpenCode SDK: https://opencode.ai/docs/sdk
// - OpenCode Server: https://open-code.ai/docs/en/server#messages
func (a *OpenCodeAdapter) sendPromptAsync(ctx context.Context, sessionID, prompt string) error {
	params := opencode.SessionPromptParams{
		Parts: opencode.F([]opencode.SessionPromptParamsPartUnion{
			opencode.SessionPromptParamsPart{
				Type: opencode.F(opencode.SessionPromptParamsPartsTypeText),
				Text: opencode.F(prompt),
			},
		}),
	}

	if a.model != nil {
		params.Model = opencode.F(opencode.SessionPromptParamsModel{
			ProviderID: opencode.F(a.model.ProviderID),
			ModelID:    opencode.F(a.model.ModelID),
		})
	}

	if err := a.client.Execute(ctx, http.MethodPost, fmt.Sprintf("session/%s/prompt_async", sessionID), params, nil); err != nil {
		return fmt.Errorf("send prompt failed: %w", err)
	}

	return nil
}

// buildBasicAuthHeader returns a Basic auth header value when password is configured.
func buildBasicAuthHeader(username, password, defaultUsername string) string {
	if password == "" {
		return ""
	}

	if username == "" {
		username = defaultUsername
	}

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + auth
}

// parseOpenCodeModel converts config like "anthropic/claude-sonnet-4-20250514"
// into the provider/model pair used by the SDK request payload.
func parseOpenCodeModel(raw string) *OpenCodeModel {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}

	return &OpenCodeModel{
		ProviderID: parts[0],
		ModelID:    parts[1],
	}
}
