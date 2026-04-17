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

// WorktreeCreateRequest represents a create-or-reuse worktree request sent to the companion service.
type WorktreeCreateRequest struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Kind    string `json:"kind"`
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	BaseRef string `json:"baseRef"`
	HeadSHA string `json:"headSHA,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

// WorktreeResult represents the managed worktree returned by the companion service.
type WorktreeResult struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Branch       string `json:"branch"`
	BaseRef      string `json:"baseRef"`
	WorktreePath string `json:"worktreePath"`
	Reused       bool   `json:"reused"`
}

type worktreeManagerHealthResponse struct {
	Status string `json:"status"`
}

// WorktreeManagerClient calls the agent-side companion service.
type WorktreeManagerClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewWorktreeManagerClient creates a new companion service client.
func NewWorktreeManagerClient(cfg config.OpenCodeConfig, timeout time.Duration) *WorktreeManagerClient {
	username := cfg.WorktreeManagerUsername
	if username == "" {
		username = "worktree-manager"
	}

	return &WorktreeManagerClient{
		baseURL:  strings.TrimSuffix(cfg.WorktreeManagerHost, "/"),
		username: username,
		password: cfg.WorktreeManagerPassword,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// CreateOrReuse creates or reuses an isolated git worktree on the agent machine.
func (c *WorktreeManagerClient) CreateOrReuse(ctx context.Context, req WorktreeCreateRequest) (*WorktreeResult, error) {
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal worktree request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/worktrees/create-or-reuse", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create worktree-manager request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("worktree-manager request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worktree-manager returned status %d: %s", resp.StatusCode, string(body))
	}

	var result WorktreeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode worktree-manager response: %w", err)
	}

	return &result, nil
}

// HealthCheck verifies the companion service is reachable.
func (c *WorktreeManagerClient) HealthCheck(ctx context.Context) error {
	status := c.HealthStatus(ctx)
	if status.Healthy {
		return nil
	}
	if status.Error == "" {
		return fmt.Errorf("worktree-manager health returned unhealthy response")
	}

	return fmt.Errorf(status.Error)
}

// HealthStatus returns structured health details for the companion service.
func (c *WorktreeManagerClient) HealthStatus(ctx context.Context) ServiceHealthStatus {
	if strings.TrimSpace(c.baseURL) == "" {
		return ServiceHealthStatus{
			Error: "worktree-manager host is not configured",
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to create worktree-manager health request: %v", err),
		}
	}

	c.setAuthHeader(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("worktree-manager health request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ServiceHealthStatus{
			Error: fmt.Sprintf("worktree-manager health returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to read worktree-manager health response: %v", err),
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return ServiceHealthStatus{Healthy: true}
	}

	var health worktreeManagerHealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to decode worktree-manager health response: %v", err),
		}
	}
	if health.Status != "" && !strings.EqualFold(health.Status, "ok") {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("worktree-manager health returned status %q", health.Status),
		}
	}

	return ServiceHealthStatus{Healthy: true}
}

func (c *WorktreeManagerClient) setAuthHeader(req *http.Request) {
	if c.password == "" {
		return
	}

	auth := base64.StdEncoding.EncodeToString([]byte(c.username + ":" + c.password))
	req.Header.Set("Authorization", "Basic "+auth)
}

// buildWorktreeCreateRequest maps a bridge task into the companion service request.
func buildWorktreeCreateRequest(task TaskContext) (WorktreeCreateRequest, error) {
	branch := getWorktreeBranch(task)

	if isPRScopedTask(task) {
		if strings.TrimSpace(task.Branch) == "" {
			return WorktreeCreateRequest{}, fmt.Errorf("missing PR head branch for worktree creation")
		}

		return WorktreeCreateRequest{
			Owner:   task.RepoOwner,
			Repo:    task.RepoName,
			Kind:    "pr_review",
			Number:  task.IssueNumber,
			Branch:  branch,
			BaseRef: task.Branch,
			HeadSHA: task.HeadSHA,
		}, nil
	}

	if strings.TrimSpace(task.DefaultBranch) == "" {
		return WorktreeCreateRequest{}, fmt.Errorf("missing default branch for issue worktree creation")
	}

	return WorktreeCreateRequest{
		Owner:   task.RepoOwner,
		Repo:    task.RepoName,
		Kind:    "issue",
		Number:  task.IssueNumber,
		Branch:  branch,
		BaseRef: task.DefaultBranch,
	}, nil
}

// getWorktreeBranch generates the managed worktree branch name for a task.
func getWorktreeBranch(task TaskContext) string {
	if isPRScopedTask(task) {
		return fmt.Sprintf("pr-%d", task.IssueNumber)
	}
	return fmt.Sprintf("issue-%d", task.IssueNumber)
}

// isPRScopedTask returns true for tasks that should use the PR worktree shape.
func isPRScopedTask(task TaskContext) bool {
	switch task.EventType {
	case "pull_request", "pr_comment", "pr_review":
		return true
	default:
		return false
	}
}
