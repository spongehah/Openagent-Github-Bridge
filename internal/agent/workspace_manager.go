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

// WorkspaceCreateRequest represents a create-or-reuse workspace request sent to the companion service.
type WorkspaceCreateRequest struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	RepoURL string `json:"repoURL"`
	Kind    string `json:"kind"`
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	BaseRef string `json:"baseRef"`
	HeadSHA string `json:"headSHA,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

// WorkspaceResult represents the managed workspace returned by the companion service.
type WorkspaceResult struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Branch       string `json:"branch"`
	BaseRef      string `json:"baseRef"`
	WorktreePath string `json:"worktreePath"`
	Reused       bool   `json:"reused"`
}

type workspaceManagerHealthResponse struct {
	Status string `json:"status"`
}

// WorkspaceManagerClient calls the agent-side companion service.
type WorkspaceManagerClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewWorkspaceManagerClient creates a new companion service client.
func NewWorkspaceManagerClient(cfg config.OpenCodeConfig, timeout time.Duration) *WorkspaceManagerClient {
	username := cfg.WorkspaceManagerUsername
	if username == "" {
		username = "workspace-manager"
	}

	return &WorkspaceManagerClient{
		baseURL:  strings.TrimSuffix(cfg.WorkspaceManagerHost, "/"),
		username: username,
		password: cfg.WorkspaceManagerPassword,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// CreateOrReuse creates or reuses an isolated git workspace on the agent machine.
func (c *WorkspaceManagerClient) CreateOrReuse(ctx context.Context, req WorkspaceCreateRequest) (*WorkspaceResult, error) {
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workspace request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/workspaces/create-or-reuse", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace-manager request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workspace-manager request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("workspace-manager returned status %d: %s", resp.StatusCode, string(body))
	}

	var result WorkspaceResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode workspace-manager response: %w", err)
	}

	return &result, nil
}

// HealthCheck verifies the companion service is reachable.
func (c *WorkspaceManagerClient) HealthCheck(ctx context.Context) error {
	status := c.HealthStatus(ctx)
	if status.Healthy {
		return nil
	}
	if status.Error == "" {
		return fmt.Errorf("workspace-manager health returned unhealthy response")
	}

	return fmt.Errorf(status.Error)
}

// HealthStatus returns structured health details for the companion service.
func (c *WorkspaceManagerClient) HealthStatus(ctx context.Context) ServiceHealthStatus {
	if strings.TrimSpace(c.baseURL) == "" {
		return ServiceHealthStatus{
			Error: "workspace-manager host is not configured",
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to create workspace-manager health request: %v", err),
		}
	}

	c.setAuthHeader(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("workspace-manager health request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ServiceHealthStatus{
			Error: fmt.Sprintf("workspace-manager health returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to read workspace-manager health response: %v", err),
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return ServiceHealthStatus{Healthy: true}
	}

	var health workspaceManagerHealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("failed to decode workspace-manager health response: %v", err),
		}
	}
	if health.Status != "" && !strings.EqualFold(health.Status, "ok") {
		return ServiceHealthStatus{
			Error: fmt.Sprintf("workspace-manager health returned status %q", health.Status),
		}
	}

	return ServiceHealthStatus{Healthy: true}
}

func (c *WorkspaceManagerClient) setAuthHeader(req *http.Request) {
	if c.password == "" {
		return
	}

	auth := base64.StdEncoding.EncodeToString([]byte(c.username + ":" + c.password))
	req.Header.Set("Authorization", "Basic "+auth)
}

// buildWorkspaceCreateRequest maps a bridge task into the companion service request.
func buildWorkspaceCreateRequest(task TaskContext) (WorkspaceCreateRequest, error) {
	branch := getManagedWorkspaceBranch(task)
	repoURL := strings.TrimSpace(task.RepoURL)
	if repoURL == "" {
		return WorkspaceCreateRequest{}, fmt.Errorf("missing repository clone URL for workspace creation")
	}

	if isPRScopedTask(task) {
		if strings.TrimSpace(task.Branch) == "" {
			return WorkspaceCreateRequest{}, fmt.Errorf("missing PR head branch for workspace creation")
		}

		return WorkspaceCreateRequest{
			Owner:   task.RepoOwner,
			Repo:    task.RepoName,
			RepoURL: repoURL,
			Kind:    "pr_review",
			Number:  task.IssueNumber,
			Branch:  branch,
			BaseRef: task.Branch,
			HeadSHA: task.HeadSHA,
		}, nil
	}

	if strings.TrimSpace(task.DefaultBranch) == "" {
		return WorkspaceCreateRequest{}, fmt.Errorf("missing default branch for issue workspace creation")
	}

	return WorkspaceCreateRequest{
		Owner:   task.RepoOwner,
		Repo:    task.RepoName,
		RepoURL: repoURL,
		Kind:    "issue",
		Number:  task.IssueNumber,
		Branch:  branch,
		BaseRef: task.DefaultBranch,
	}, nil
}

// getManagedWorkspaceBranch generates the managed workspace branch name for a task.
func getManagedWorkspaceBranch(task TaskContext) string {
	if isPRScopedTask(task) {
		return fmt.Sprintf("pr-%d", task.IssueNumber)
	}
	return fmt.Sprintf("issue-%d", task.IssueNumber)
}

// isPRScopedTask returns true for tasks that should use the PR workspace shape.
func isPRScopedTask(task TaskContext) bool {
	switch task.EventType {
	case "pull_request", "pr_comment", "pr_review":
		return true
	default:
		return false
	}
}
