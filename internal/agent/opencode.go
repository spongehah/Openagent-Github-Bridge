// Package agent provides abstractions for AI agent interactions.
// This file implements the OpenCode adapter for dispatching tasks to the OpenCode server.
//
// References:
// - OpenCode Server API: https://open-code.ai/docs/en/server
// - OpenCode SDK: https://opencode.ai/docs/sdk
// - Worktree Plugin: https://github.com/kdcokenny/opencode-worktree
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openagent/github-bridge/internal/config"
)

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
	baseURL    string
	username   string // HTTP Basic Auth username (default: "opencode")
	password   string // HTTP Basic Auth password
	timeout    time.Duration
	httpClient *http.Client
}

// NewOpenCodeAdapter creates a new OpenCode adapter.
//
// Authentication:
// If password is set, HTTP Basic Auth is used.
// Reference: https://open-code.ai/docs/en/server#authentication
func NewOpenCodeAdapter(cfg config.OpenCodeConfig) *OpenCodeAdapter {
	username := cfg.Username
	if username == "" {
		username = "opencode" // Default username per OpenCode docs
	}

	return &OpenCodeAdapter{
		baseURL:  strings.TrimSuffix(cfg.Host, "/"),
		username: username,
		password: cfg.Password,
		timeout:  cfg.Timeout,
		httpClient: &http.Client{
			Timeout: 30 * time.Second, // Short timeout for dispatch only
		},
	}
}

// setAuthHeader sets HTTP Basic Auth header if password is configured.
// Reference: https://open-code.ai/docs/en/server#authentication
func (a *OpenCodeAdapter) setAuthHeader(req *http.Request) {
	if a.password != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(a.username + ":" + a.password))
		req.Header.Set("Authorization", "Basic "+auth)
	}
}

// ============================================================================
// OpenCode API Request/Response Structures
// Reference: https://open-code.ai/docs/en/server#apis
// ============================================================================

// SessionCreateRequest represents a request to create a new session.
// API: POST /session
// Reference: https://open-code.ai/docs/en/server#sessions
type SessionCreateRequest struct {
	ParentID string `json:"parentID,omitempty"` // Parent session ID for forking
	Title    string `json:"title,omitempty"`    // Session title
}

// SessionResponse represents a session object from OpenCode.
// Reference: https://open-code.ai/docs/en/server#sessions
type SessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// MessagePart represents a part of a message.
// Reference: https://opencode.ai/docs/sdk#messages
type MessagePart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// PromptRequest represents a request to send a message to a session.
// API: POST /session/:id/message (sync) or POST /session/:id/prompt_async (async)
// Reference: https://open-code.ai/docs/en/server#messages
type PromptRequest struct {
	Parts   []MessagePart `json:"parts"`             // Message parts
	NoReply bool          `json:"noReply,omitempty"` // If true, returns immediately without waiting for response
}

// WorktreeCreateRequest represents a request to create a worktree via slash command.
// Uses the /workspace slash command endpoint.
// API: POST /session/:id/command
// Reference: https://open-code.ai/docs/en/server#messages
type CommandRequest struct {
	Command   string            `json:"command"`   // e.g., "/workspace"
	Arguments map[string]string `json:"arguments"` // e.g., {"branch": "issue-123"}
}

// ============================================================================
// Main Dispatch Logic
// ============================================================================

// DispatchTask sends a task to OpenCode and returns immediately.
//
// Flow:
// 1. Reuse existing session OR create new session for this issue/PR
// 2. If new session: Create worktree for isolation (based on repo's default branch)
// 3. Send prompt asynchronously (fire-and-forget)
//
// Session Reuse:
// - If task.AgentSessionID is set, reuse that session (continuing conversation)
// - If empty, create a new session and worktree
//
// Worktree Creation:
// - Based on the repository's default branch (e.g., main)
// - Branch name format: {type}-{number} (e.g., issue-123, pr-456)
// - Requires OpenCode to have the repository pre-cloned
//
// OpenCode handles execution, GitHub interaction, and PR creation asynchronously.
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, task TaskContext) (*DispatchResult, error) {
	var sessionID string
	isNewSession := task.AgentSessionID == ""

	if isNewSession {
		// Step 1a: Create a new session
		// API: POST /session
		session, err := a.createSession(ctx, task.SessionKey)
		if err != nil {
			return &DispatchResult{
				Dispatched: false,
				Error:      fmt.Sprintf("failed to create session: %v", err),
			}, err
		}
		sessionID = session.ID

		// Step 2: Create worktree for isolation (only for new sessions)
		// Uses /workspace command to create isolated git worktree
		// Based on the repository's default branch
		worktreeBranch := a.getWorktreeBranch(task)
		if err := a.createWorktree(ctx, sessionID, task, worktreeBranch); err != nil {
			// Log warning but continue - worktree creation is best-effort
			// OpenCode may work in main workspace if worktree fails
			fmt.Printf("[OpenCode] Warning: failed to create worktree %s: %v\n", worktreeBranch, err)
		}
	} else {
		// Step 1b: Reuse existing session
		sessionID = task.AgentSessionID
		fmt.Printf("[OpenCode] Reusing existing session: %s\n", sessionID)
	}

	// Step 3: Build the prompt with full context
	prompt := a.buildPrompt(task)

	// Step 4: Send prompt asynchronously (fire-and-forget)
	// API: POST /session/:id/prompt_async
	// Returns 204 No Content immediately
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

// getWorktreeBranch generates the worktree branch name for a task.
// Format: {type}-{number} (e.g., "issue-123", "pr-456")
func (a *OpenCodeAdapter) getWorktreeBranch(task TaskContext) string {
	taskType := "issue"
	if strings.Contains(task.EventType, "pull_request") {
		taskType = "pr"
	}
	return fmt.Sprintf("%s-%d", taskType, task.IssueNumber)
}

// buildPrompt constructs the full prompt with repository and issue context.
func (a *OpenCodeAdapter) buildPrompt(task TaskContext) string {
	var sb strings.Builder

	sb.WriteString("# Task Context\n\n")
	sb.WriteString(fmt.Sprintf("**Repository:** %s/%s\n", task.RepoOwner, task.RepoName))
	sb.WriteString(fmt.Sprintf("**Clone URL:** %s\n", task.RepoURL))
	sb.WriteString(fmt.Sprintf("**Default Branch:** %s\n", task.DefaultBranch))
	sb.WriteString(fmt.Sprintf("**Issue/PR Number:** #%d\n", task.IssueNumber))
	sb.WriteString(fmt.Sprintf("**Triggered by:** @%s\n", task.Sender))

	if len(task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(task.Labels, ", ")))
	}

	sb.WriteString("\n---\n\n")

	// Include the user-provided prompt (from PromptBuilder)
	sb.WriteString(task.Prompt)

	return sb.String()
}

// ============================================================================
// OpenCode API Methods
// ============================================================================

// createSession creates a new session in OpenCode.
// API: POST /session
// Reference: https://open-code.ai/docs/en/server#sessions
func (a *OpenCodeAdapter) createSession(ctx context.Context, title string) (*SessionResponse, error) {
	reqBody := SessionCreateRequest{
		Title: title,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/session", bytes.NewBuffer(jsonBody))
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

// createWorktree creates an isolated git worktree for the session.
// Uses the /workspace slash command.
//
// Worktree is created based on the repository's default branch (e.g., main).
// The new branch name corresponds to the issue/PR number (e.g., issue-123).
//
// Prerequisites:
// - OpenCode must have the repository pre-cloned in its workspace
// - Repository path: ~/repos/{owner}/{repo} (or configured in OpenCode)
//
// API: POST /session/:id/command
// Reference: https://open-code.ai/docs/en/server#messages
// Worktree Plugin: https://github.com/kdcokenny/opencode-worktree
func (a *OpenCodeAdapter) createWorktree(ctx context.Context, sessionID string, task TaskContext, branch string) error {
	// Build worktree creation arguments
	// - repo: Repository identifier (owner/repo)
	// - base: Base branch to create worktree from (e.g., main)
	// - branch: New branch name for the worktree (e.g., issue-123)
	reqBody := CommandRequest{
		Command: "/workspace",
		Arguments: map[string]string{
			"repo":   fmt.Sprintf("%s/%s", task.RepoOwner, task.RepoName),
			"base":   task.DefaultBranch,
			"branch": branch,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal command request: %w", err)
	}

	url := fmt.Sprintf("%s/session/%s/command", a.baseURL, sessionID)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
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

	// Accept any 2xx status as success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create worktree failed with status %d: %s", resp.StatusCode, string(body))
}

// sendPromptAsync sends a prompt to the session asynchronously (fire-and-forget).
// API: POST /session/:id/prompt_async
// Reference: https://open-code.ai/docs/en/server#messages
// Returns 204 No Content immediately without waiting for AI response.
func (a *OpenCodeAdapter) sendPromptAsync(ctx context.Context, sessionID, prompt string) error {
	reqBody := PromptRequest{
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
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
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

	// 204 No Content is the expected response for async prompt
	// Also accept 200, 201, 202 as success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("send prompt failed with status %d: %s", resp.StatusCode, string(body))
}

// HealthCheck verifies the OpenCode server is reachable.
// API: GET /global/health
// Reference: https://open-code.ai/docs/en/server#global
func (a *OpenCodeAdapter) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+"/global/health", nil)
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

	return nil
}
