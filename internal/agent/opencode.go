// Package agent provides abstractions for AI agent interactions.
// This file implements the OpenCode adapter for dispatching tasks to the OpenCode server.
//
// References:
// - OpenCode Server API: https://open-code.ai/docs/en/server
// - OpenCode SDK: https://opencode.ai/docs/sdk
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openagent/github-bridge/internal/config"
)

const defaultOpenCodeTimeout = 30 * time.Second

// OpenCodeAdapter implements the Agent interface using OpenCode Server HTTP API.
//
// It dispatches tasks to OpenCode and returns immediately (fire-and-forget).
// OpenCode is responsible for:
// - Executing the task in an isolated git worktree
// - Creating PRs or posting comments to GitHub
// - Managing its own GitHub authentication
//
// API Reference: https://open-code.ai/docs/en/server#apis
type OpenCodeAdapter struct {
	baseURL         string
	username        string // HTTP Basic Auth username (default: "opencode")
	password        string // HTTP Basic Auth password
	timeout         time.Duration
	httpClient      *http.Client
	model           *OpenCodeModel
	worktreeManager *WorktreeManagerClient
}

// OpenCodeModel represents the model payload used by the HTTP API.
// Reference: https://opencode.ai/docs/sdk
type OpenCodeModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// SessionCreateRequest represents a request to create a new session.
// API: POST /session?directory=<path>
// Reference: https://open-code.ai/docs/en/server#sessions
type SessionCreateRequest struct {
	Title    string `json:"title,omitempty"`    // Session title
	ParentID string `json:"parentID,omitempty"` // Optional parent session ID
}

// SessionResponse represents a session object from OpenCode.
// Reference: https://open-code.ai/docs/en/server#sessions
type SessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// MessagePart represents a request message part.
// Reference: https://opencode.ai/docs/sdk#messages
type MessagePart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// MessageRequest represents a request to send a message to a session.
// API: POST /session/:id/prompt_async
// Reference: https://open-code.ai/docs/en/server#messages
type MessageRequest struct {
	Model *OpenCodeModel `json:"model,omitempty"`
	Parts []MessagePart  `json:"parts"`
}

// NewOpenCodeAdapter creates a new OpenCode adapter.
//
// Authentication:
// If password is set, HTTP Basic Auth is used.
// Reference: https://open-code.ai/docs/en/server#authentication
func NewOpenCodeAdapter(cfg config.OpenCodeConfig) *OpenCodeAdapter {
	username := cfg.Username
	if username == "" {
		username = "opencode"
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultOpenCodeTimeout
	}

	return &OpenCodeAdapter{
		baseURL:  strings.TrimSuffix(cfg.Host, "/"),
		username: username,
		password: cfg.Password,
		timeout:  timeout,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		model:           parseOpenCodeModel(cfg.DefaultModel),
		worktreeManager: NewWorktreeManagerClient(cfg, timeout),
	}
}

// DispatchTask sends a task to OpenCode and returns immediately.
//
// Flow:
// 1. Reuse existing session OR create a new isolated session for this issue/PR
// 2. If new session: call the companion worktree-manager service, then create an OpenCode session in that path
// 3. Send prompt asynchronously (fire-and-forget)
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, task TaskContext) (*DispatchResult, error) {
	sessionID := task.AgentSessionID

	if sessionID == "" {
		session, err := a.createIsolatedSession(ctx, task)
		if err != nil {
			return &DispatchResult{
				Dispatched: false,
				Error:      fmt.Sprintf("failed to create isolated session: %v", err),
			}, err
		}
		sessionID = session.ID
	} else {
		fmt.Printf("[OpenCode] Reusing existing session: %s\n", sessionID)
	}

	prompt := a.buildPrompt(task)
	if err := a.sendPromptAsync(ctx, sessionID, prompt); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/global/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	a.setAuthHeader(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	if a.worktreeManager == nil {
		return fmt.Errorf("worktree-manager client is not configured")
	}

	if err := a.worktreeManager.HealthCheck(ctx); err != nil {
		return fmt.Errorf("worktree-manager health check failed: %w", err)
	}

	return nil
}

// createIsolatedSession prepares the worktree through the companion service
// and then creates the long-lived OpenCode session inside that directory.
func (a *OpenCodeAdapter) createIsolatedSession(ctx context.Context, task TaskContext) (*SessionResponse, error) {
	if a.worktreeManager == nil || a.worktreeManager.baseURL == "" {
		return nil, fmt.Errorf("worktree-manager host is not configured")
	}

	worktreeReq, err := buildWorktreeCreateRequest(task)
	if err != nil {
		return nil, err
	}

	worktreeResult, err := a.worktreeManager.CreateOrReuse(ctx, worktreeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create or reuse worktree: %w", err)
	}
	if strings.TrimSpace(worktreeResult.WorktreePath) == "" {
		return nil, fmt.Errorf("worktree-manager returned an empty worktreePath")
	}

	finalSession, err := a.createSession(ctx, task.SessionKey, worktreeResult.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create final session in worktree %s: %w", worktreeResult.WorktreePath, err)
	}

	return finalSession, nil
}

// buildPrompt constructs the full prompt with repository and issue context.
func (a *OpenCodeAdapter) buildPrompt(task TaskContext) string {
	var sb strings.Builder

	sb.WriteString("# Task Context\n\n")
	sb.WriteString(fmt.Sprintf("**Repository:** %s/%s\n", task.RepoOwner, task.RepoName))
	sb.WriteString(fmt.Sprintf("**Clone URL:** %s\n", task.RepoURL))
	if task.DefaultBranch != "" {
		sb.WriteString(fmt.Sprintf("**Default Branch:** %s\n", task.DefaultBranch))
	}
	if task.BaseBranch != "" {
		sb.WriteString(fmt.Sprintf("**Base Branch:** %s\n", task.BaseBranch))
	}
	if task.Branch != "" {
		sb.WriteString(fmt.Sprintf("**Working Branch:** %s\n", task.Branch))
	}
	if task.HeadSHA != "" {
		sb.WriteString(fmt.Sprintf("**Head SHA:** %s\n", task.HeadSHA))
	}
	sb.WriteString(fmt.Sprintf("**Issue/PR Number:** #%d\n", task.IssueNumber))
	sb.WriteString(fmt.Sprintf("**Triggered by:** @%s\n", task.Sender))

	if len(task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(task.Labels, ", ")))
	}

	sb.WriteString("\n---\n\n")
	sb.WriteString(task.Prompt)

	return sb.String()
}

// createSession creates a new session in OpenCode with the specified working directory.
// API: POST /session?directory=<path>
// Reference: https://open-code.ai/docs/en/server#sessions
// Note: The working directory is passed as a query parameter, not in the request body.
func (a *OpenCodeAdapter) createSession(ctx context.Context, title, directory string) (*SessionResponse, error) {
	reqBody := SessionCreateRequest{
		Title: title,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session request: %w", err)
	}

	// directory is a query param per OpenCode server API spec
	params := url.Values{}
	params.Set("directory", directory)
	reqURL := fmt.Sprintf("%s/session?%s", a.baseURL, params.Encode())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	a.setAuthHeader(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create session failed with status %d: %s", resp.StatusCode, string(body))
	}

	var session SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode session response: %w", err)
	}

	return &session, nil
}

// sendPromptAsync sends a prompt to the session asynchronously (fire-and-forget).
// API: POST /session/:id/prompt_async
// Reference: https://open-code.ai/docs/en/server#messages
func (a *OpenCodeAdapter) sendPromptAsync(ctx context.Context, sessionID, prompt string) error {
	reqBody := MessageRequest{
		Model: a.model,
		Parts: []MessagePart{
			{
				Type: "text",
				Text: prompt,
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal prompt request: %w", err)
	}

	url := fmt.Sprintf("%s/session/%s/prompt_async", a.baseURL, sessionID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	a.setAuthHeader(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("send prompt failed with status %d: %s", resp.StatusCode, string(body))
}

// setAuthHeader sets HTTP Basic Auth header if password is configured.
// Reference: https://open-code.ai/docs/en/server#authentication
func (a *OpenCodeAdapter) setAuthHeader(req *http.Request) {
	if a.password == "" {
		return
	}

	auth := base64.StdEncoding.EncodeToString([]byte(a.username + ":" + a.password))
	req.Header.Set("Authorization", "Basic "+auth)
}

// parseOpenCodeModel converts config like "anthropic/claude-sonnet-4-20250514"
// into the object payload expected by the local OpenCode HTTP runtime.
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
