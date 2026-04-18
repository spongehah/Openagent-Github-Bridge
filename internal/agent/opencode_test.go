package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"

	"github.com/openagent/github-bridge/internal/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body any) *http.Response {
	var payload []byte
	switch v := body.(type) {
	case string:
		payload = []byte(v)
	default:
		payload, _ = json.Marshal(v)
	}

	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(payload))),
	}
}

func newTestAdapter(model string, openCodeTransport, workspaceTransport roundTripperFunc) *OpenCodeAdapter {
	return &OpenCodeAdapter{
		client: opencode.NewClient(
			option.WithBaseURL("http://opencode.local"),
			option.WithHTTPClient(&http.Client{Transport: openCodeTransport}),
		),
		model: parseOpenCodeModel(model),
		workspaceManager: &WorkspaceManagerClient{
			baseURL:    "http://workspace.local",
			httpClient: &http.Client{Transport: workspaceTransport},
		},
	}
}

func TestBuildWorkspaceCreateRequestForIssue(t *testing.T) {
	t.Parallel()

	args, err := buildWorkspaceCreateRequest(TaskContext{
		RepoOwner:     "openagent",
		RepoName:      "github-bridge",
		RepoURL:       "git@github.com:openagent/github-bridge.git",
		EventType:     "issue",
		IssueNumber:   42,
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("buildWorkspaceCreateRequest returned error: %v", err)
	}

	if args.Kind != "issue" {
		t.Fatalf("expected issue kind, got %q", args.Kind)
	}
	if args.RepoURL != "git@github.com:openagent/github-bridge.git" {
		t.Fatalf("expected repo URL to be preserved, got %q", args.RepoURL)
	}
	if args.Branch != "issue-42" {
		t.Fatalf("expected issue branch, got %q", args.Branch)
	}
	if args.BaseRef != "main" {
		t.Fatalf("expected main base ref, got %q", args.BaseRef)
	}
	if args.HeadSHA != "" {
		t.Fatalf("expected empty head sha, got %q", args.HeadSHA)
	}
}

func TestBuildWorkspaceCreateRequestForPR(t *testing.T) {
	t.Parallel()

	args, err := buildWorkspaceCreateRequest(TaskContext{
		RepoOwner:   "openagent",
		RepoName:    "github-bridge",
		RepoURL:     "git@github.com:openagent/github-bridge.git",
		EventType:   "pr_review",
		IssueNumber: 128,
		Branch:      "feature/smaller-worktree",
		HeadSHA:     "abc123",
	})
	if err != nil {
		t.Fatalf("buildWorkspaceCreateRequest returned error: %v", err)
	}

	if args.Kind != "pr_review" {
		t.Fatalf("expected pr_review kind, got %q", args.Kind)
	}
	if args.RepoURL != "git@github.com:openagent/github-bridge.git" {
		t.Fatalf("expected repo URL to be preserved, got %q", args.RepoURL)
	}
	if args.Branch != "pr-128" {
		t.Fatalf("expected PR branch, got %q", args.Branch)
	}
	if args.BaseRef != "feature/smaller-worktree" {
		t.Fatalf("expected PR head ref as baseRef, got %q", args.BaseRef)
	}
	if args.HeadSHA != "abc123" {
		t.Fatalf("expected head sha, got %q", args.HeadSHA)
	}
}

func TestBuildSessionTitle(t *testing.T) {
	t.Parallel()

	got := buildSessionTitle("openagent/github-bridge/issue/42", time.Date(2026, time.April, 17, 16, 30, 30, 0, time.FixedZone("UTC+8", 8*60*60)))
	want := "openagent/github-bridge/issue/42-20260417-163030"
	if got != want {
		t.Fatalf("expected session title %q, got %q", want, got)
	}
}

func TestOpenCodeAdapterDispatchTaskReusesSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	workspaceTransport := roundTripperFunc(func(w *http.Request) (*http.Response, error) {
		t.Fatalf("workspace manager should not be called when reusing a session")
		return nil, nil
	})

	var promptChecked bool
	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/session-456/prompt_async" {
			t.Fatalf("unexpected OpenCode request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode prompt request: %v", err)
		}
		parts, ok := body["parts"].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("expected one prompt part, got %#v", body["parts"])
		}
		part, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("expected text part object, got %#v", parts[0])
		}
		if part["text"] != "Fix the bug" {
			t.Fatalf("expected task prompt to be sent without wrapper, got %#v", part["text"])
		}
		if _, exists := body["agent"]; exists {
			t.Fatalf("did not expect agent override for default dispatch, got %#v", body["agent"])
		}

		promptChecked = true
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)

	result, err := adapter.DispatchTask(ctx, TaskContext{
		AgentSessionID: "session-456",
		RepoOwner:      "openagent",
		RepoName:       "github-bridge",
		IssueNumber:    42,
		Sender:         "alice",
		Prompt:         "Fix the bug",
	})
	if err != nil {
		t.Fatalf("DispatchTask returned error: %v", err)
	}

	if !result.Dispatched || result.TaskID != "session-456" {
		t.Fatalf("unexpected dispatch result: %#v", result)
	}
	if !promptChecked {
		t.Fatalf("expected prompt request to be sent")
	}
}

func TestOpenCodeAdapterDispatchTaskRefreshesPRWorkspaceWhenReusingSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var workspaceChecked bool
	workspaceTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/workspaces/create-or-reuse" {
			t.Fatalf("unexpected workspace request: %s %s", r.Method, r.URL.Path)
		}

		var req WorkspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode workspace request: %v", err)
		}

		if req.Kind != "pr_review" {
			t.Fatalf("expected pr_review workspace kind, got %q", req.Kind)
		}
		if req.RepoURL != "git@github.com:openagent/github-bridge.git" {
			t.Fatalf("expected repo URL in workspace request, got %q", req.RepoURL)
		}
		if req.Branch != "pr-42" {
			t.Fatalf("expected managed PR branch, got %q", req.Branch)
		}
		if req.BaseRef != "feature/latest-head" {
			t.Fatalf("expected PR head ref as baseRef, got %q", req.BaseRef)
		}
		if req.HeadSHA != "deadbeef" {
			t.Fatalf("expected latest head sha, got %q", req.HeadSHA)
		}

		workspaceChecked = true
		return jsonResponse(http.StatusOK, WorkspaceResult{
			WorktreePath: "/tmp/worktrees/pr-42",
			Reused:       true,
		}), nil
	})

	var promptChecked bool
	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/session-456/prompt_async" {
			t.Fatalf("unexpected OpenCode request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode prompt request: %v", err)
		}
		parts, ok := body["parts"].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("expected one prompt part, got %#v", body["parts"])
		}
		part, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("expected text part object, got %#v", parts[0])
		}
		if part["text"] != "Review the PR" {
			t.Fatalf("expected task prompt to be sent without wrapper, got %#v", part["text"])
		}
		if _, exists := body["agent"]; exists {
			t.Fatalf("did not expect agent override for PR review dispatch, got %#v", body["agent"])
		}

		promptChecked = true
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)

	result, err := adapter.DispatchTask(ctx, TaskContext{
		AgentSessionID: "session-456",
		RepoOwner:      "openagent",
		RepoName:       "github-bridge",
		RepoURL:        "git@github.com:openagent/github-bridge.git",
		IssueNumber:    42,
		Branch:         "feature/latest-head",
		HeadSHA:        "deadbeef",
		Sender:         "alice",
		Prompt:         "Review the PR",
		EventType:      "pr_review",
	})
	if err != nil {
		t.Fatalf("DispatchTask returned error: %v", err)
	}

	if !result.Dispatched || result.TaskID != "session-456" {
		t.Fatalf("unexpected dispatch result: %#v", result)
	}
	if !workspaceChecked {
		t.Fatalf("expected PR workspace refresh before reusing session")
	}
	if !promptChecked {
		t.Fatalf("expected prompt request to be sent")
	}
}

func TestDispatchTaskSendsRawTaskPrompt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspaceTransport := roundTripperFunc(func(w *http.Request) (*http.Response, error) {
		t.Fatalf("workspace manager should not be called when reusing a session")
		return nil, nil
	})

	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/session-raw/prompt_async" {
			t.Fatalf("unexpected OpenCode request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode prompt request: %v", err)
		}
		parts, ok := body["parts"].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("expected one prompt part, got %#v", body["parts"])
		}
		part, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("expected text part object, got %#v", parts[0])
		}
		if part["text"] != "# Mandatory Execution Requirements\n\nOnly prompt.go should define this content." {
			t.Fatalf("expected raw task prompt, got %#v", part["text"])
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)
	result, err := adapter.DispatchTask(ctx, TaskContext{
		AgentSessionID: "session-raw",
		Prompt:         "# Mandatory Execution Requirements\n\nOnly prompt.go should define this content.",
	})
	if err != nil {
		t.Fatalf("DispatchTask returned error: %v", err)
	}
	if !result.Dispatched || result.TaskID != "session-raw" {
		t.Fatalf("unexpected dispatch result: %#v", result)
	}
}

func TestSendPromptAsyncDoesNotSetNoReply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspaceTransport := roundTripperFunc(func(w *http.Request) (*http.Response, error) {
		t.Fatalf("workspace manager should not be called when reusing a session")
		return nil, nil
	})

	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/session-async/prompt_async" {
			t.Fatalf("unexpected OpenCode request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode prompt request: %v", err)
		}
		if _, exists := body["noReply"]; exists {
			t.Fatalf("prompt_async payload must not set noReply; OpenCode treats noReply as context-only")
		}

		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)
	if err := adapter.sendPromptAsync(ctx, "session-async", "Plan the fix", ""); err != nil {
		t.Fatalf("sendPromptAsync returned error: %v", err)
	}
}

func TestSendPromptAsyncIncludesAgentOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspaceTransport := roundTripperFunc(func(w *http.Request) (*http.Response, error) {
		t.Fatalf("workspace manager should not be called when reusing a session")
		return nil, nil
	})

	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/session-plan/prompt_async" {
			t.Fatalf("unexpected OpenCode request: %s %s", r.Method, r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode prompt request: %v", err)
		}
		if body["agent"] != "plan" {
			t.Fatalf("expected agent override plan, got %#v", body["agent"])
		}

		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)
	if err := adapter.sendPromptAsync(ctx, "session-plan", "Plan the fix", "plan"); err != nil {
		t.Fatalf("sendPromptAsync returned error: %v", err)
	}
}

func TestOpenCodeAdapterHealthStatusUsesSDKAndAuthHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("sdk-user:secret"))

	workspaceTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected workspace health path: %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/global/health" {
			t.Fatalf("unexpected health path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != expectedAuth {
			t.Fatalf("unexpected auth header: %q", got)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"healthy": true,
			"version": "1.0.0",
		}), nil
	})

	adapter := NewOpenCodeAdapter(config.OpenCodeConfig{
		Host:     "http://opencode.local",
		Username: "sdk-user",
		Password: "secret",
	})
	adapter.client = opencode.NewClient(
		option.WithBaseURL("http://opencode.local"),
		option.WithHeader("Authorization", expectedAuth),
		option.WithHTTPClient(&http.Client{Transport: openCodeTransport}),
	)
	adapter.workspaceManager = &WorkspaceManagerClient{
		baseURL:    "http://workspace.local",
		httpClient: &http.Client{Transport: workspaceTransport},
	}

	report := adapter.HealthStatus(ctx)
	if !report.Healthy {
		t.Fatalf("HealthStatus reported unhealthy state: %#v", report)
	}

	repositoryStatus, ok := report.Repositories[defaultHealthRepository]
	if !ok {
		t.Fatalf("expected %q repository status, got %#v", defaultHealthRepository, report.Repositories)
	}
	if !repositoryStatus.OpenCode.Healthy {
		t.Fatalf("expected OpenCode to be healthy, got %#v", repositoryStatus.OpenCode)
	}
	if repositoryStatus.OpenCode.Version != "1.0.0" {
		t.Fatalf("expected OpenCode version to be reported, got %#v", repositoryStatus.OpenCode)
	}
	if !repositoryStatus.WorkspaceManager.Healthy {
		t.Fatalf("expected workspace-manager to be healthy, got %#v", repositoryStatus.WorkspaceManager)
	}

	if err := adapter.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck returned error: %v", err)
	}
}

func TestOpenCodeAdapterHealthStatusIncludesDependencyFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	workspaceTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected workspace health path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{
			"error": "workspace down",
		}), nil
	})

	openCodeTransport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/global/health" {
			t.Fatalf("unexpected health path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"healthy": false,
			"version": "1.0.1",
		}), nil
	})

	adapter := newTestAdapter("", openCodeTransport, workspaceTransport)

	report := adapter.HealthStatus(ctx)
	if report.Healthy {
		t.Fatalf("expected unhealthy report, got %#v", report)
	}

	repositoryStatus := report.Repositories[defaultHealthRepository]
	if repositoryStatus.Healthy {
		t.Fatalf("expected repository health to be unhealthy, got %#v", repositoryStatus)
	}
	if repositoryStatus.OpenCode.Error != "health check returned unhealthy response" {
		t.Fatalf("unexpected OpenCode error: %#v", repositoryStatus.OpenCode)
	}
	if !strings.Contains(repositoryStatus.WorkspaceManager.Error, "workspace-manager health returned status 503") {
		t.Fatalf("unexpected workspace-manager error: %#v", repositoryStatus.WorkspaceManager)
	}

	if err := adapter.HealthCheck(ctx); err == nil {
		t.Fatalf("expected HealthCheck to fail")
	}
}

func TestMultiRepoOpenCodeAdapterHealthStatusChecksConfiguredRepositories(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := &config.Config{
		Repositories: map[string]config.RepositoryConfig{
			"openagent/healthy":   {},
			"openagent/unhealthy": {},
		},
	}
	adapter := NewMultiRepoOpenCodeAdapter(cfg)
	adapter.adapters["openagent/healthy"] = newTestAdapter(
		"",
		roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, map[string]any{
				"healthy": true,
				"version": "1.0.0",
			}), nil
		}),
		roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, map[string]any{
				"status": "ok",
			}), nil
		}),
	)
	adapter.adapters["openagent/unhealthy"] = newTestAdapter(
		"",
		roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, map[string]any{
				"healthy": true,
				"version": "1.0.0",
			}), nil
		}),
		roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, map[string]any{
				"error": "down",
			}), nil
		}),
	)

	report := adapter.HealthStatus(ctx)
	if report.Healthy {
		t.Fatalf("expected multi-repo health report to be unhealthy, got %#v", report)
	}
	if !report.Repositories["openagent/healthy"].Healthy {
		t.Fatalf("expected healthy repository status, got %#v", report.Repositories["openagent/healthy"])
	}
	if report.Repositories["openagent/unhealthy"].Healthy {
		t.Fatalf("expected unhealthy repository status, got %#v", report.Repositories["openagent/unhealthy"])
	}
}
