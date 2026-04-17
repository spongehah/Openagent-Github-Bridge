package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ghapi "github.com/google/go-github/v70/github"
	"github.com/openagent/github-bridge/internal/config"
	ghwebhook "github.com/openagent/github-bridge/internal/github"
	"github.com/openagent/github-bridge/internal/queue"
)

type fakePullRequestGetter struct {
	getPullRequest func(ctx context.Context, owner, repo string, number int) (*ghapi.PullRequest, error)
}

func (f fakePullRequestGetter) GetPullRequest(ctx context.Context, owner, repo string, number int) (*ghapi.PullRequest, error) {
	return f.getPullRequest(ctx, owner, repo, number)
}

func TestHandleWebhookReturns200ForNonActionableEvent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte(`{
		"action":"edited",
		"issue":{"number":42,"title":"Bug","body":"Body","state":"open","user":{"login":"alice"},"labels":[]},
		"repository":{"full_name":"openagent/github-bridge","owner":{"login":"openagent"},"name":"github-bridge","default_branch":"main","clone_url":"https://github.com/openagent/github-bridge.git"},
		"sender":{"login":"alice"}
	}`)

	recorder, taskQueue := performWebhookRequest(t, "issues", payload)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", recorder.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["message"] != "event not actionable" {
		t.Fatalf("expected non-actionable response, got %#v", response["message"])
	}

	if _, exists := response["task_id"]; exists {
		t.Fatalf("expected no task_id in response, got %#v", response["task_id"])
	}

	if taskQueue.Len() != 0 {
		t.Fatalf("expected queue length 0, got %d", taskQueue.Len())
	}
}

func TestHandleWebhookQueuesActionableEvent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte(`{
		"action":"opened",
		"issue":{"number":42,"title":"Bug","body":"Body","state":"open","user":{"login":"alice"},"labels":[{"name":"ai-fix"}]},
		"repository":{"full_name":"openagent/github-bridge","owner":{"login":"openagent"},"name":"github-bridge","default_branch":"main","clone_url":"https://github.com/openagent/github-bridge.git"},
		"sender":{"login":"alice"}
	}`)

	recorder, taskQueue := performWebhookRequest(t, "issues", payload)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202 status, got %d", recorder.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["message"] != "event received and queued" {
		t.Fatalf("expected queued response, got %#v", response["message"])
	}

	taskID, ok := response["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatalf("expected non-empty task_id, got %#v", response["task_id"])
	}

	if taskQueue.Len() != 1 {
		t.Fatalf("expected queue length 1, got %d", taskQueue.Len())
	}
}

func TestHandleWebhookQueuesAIPlanLabeledEvent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte(`{
		"action":"labeled",
		"issue":{"number":42,"title":"Bug","body":"Body","state":"open","user":{"login":"alice"},"labels":[{"name":"ai-plan"}]},
		"repository":{"full_name":"openagent/github-bridge","owner":{"login":"openagent"},"name":"github-bridge","default_branch":"main","clone_url":"https://github.com/openagent/github-bridge.git"},
		"sender":{"login":"alice"}
	}`)

	recorder, taskQueue := performWebhookRequest(t, "issues", payload)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202 status, got %d", recorder.Code)
	}
	if taskQueue.Len() != 1 {
		t.Fatalf("expected queue length 1, got %d", taskQueue.Len())
	}
}

func TestHandleWebhookQueuesGoIssueCommentEvent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte(`{
		"action":"created",
		"comment":{"id":123,"body":"/go please implement retry","user":{"login":"alice"}},
		"issue":{"number":42,"title":"Bug","body":"Body","state":"open","user":{"login":"alice"}},
		"repository":{"full_name":"openagent/github-bridge","owner":{"login":"openagent"},"name":"github-bridge","default_branch":"main","clone_url":"https://github.com/openagent/github-bridge.git"},
		"sender":{"login":"alice"}
	}`)

	recorder, taskQueue := performWebhookRequest(t, "issue_comment", payload)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202 status, got %d", recorder.Code)
	}
	if taskQueue.Len() != 1 {
		t.Fatalf("expected queue length 1, got %d", taskQueue.Len())
	}
}

func TestHandleWebhookIgnoresGoCommentInPullRequestConversation(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte(`{
		"action":"created",
		"comment":{"id":123,"body":"/go please implement retry","user":{"login":"alice"}},
		"issue":{"number":42,"title":"Bug","body":"Body","state":"open","pull_request":{"url":"https://api.github.com/repos/openagent/github-bridge/pulls/42"},"user":{"login":"alice"}},
		"repository":{"full_name":"openagent/github-bridge","owner":{"login":"openagent"},"name":"github-bridge","default_branch":"main","clone_url":"https://github.com/openagent/github-bridge.git"},
		"sender":{"login":"alice"}
	}`)

	recorder, taskQueue := performWebhookRequest(t, "issue_comment", payload)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", recorder.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["message"] != "event not actionable" {
		t.Fatalf("expected non-actionable response, got %#v", response["message"])
	}

	if taskQueue.Len() != 0 {
		t.Fatalf("expected queue length 0, got %d", taskQueue.Len())
	}
}

func TestCreateTaskFromPRReviewCommentIncludesLatestHeadContext(t *testing.T) {
	t.Parallel()

	handler := NewWebhookHandler(config.GitHubConfig{WebhookSecret: "test-secret"}, config.TriggerConfig{Prefix: "@ogb-bot"}, queue.NewTaskQueue(1))
	event := &ghwebhook.WebhookEvent{
		Type:   "pull_request_review_comment",
		Action: "created",
		Payload: &ghwebhook.PullRequestReviewCommentEvent{
			Action: "created",
			Comment: struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
				Path string `json:"path"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}{
				ID:   123,
				Body: "Looks good",
				Path: "internal/handler/webhook.go",
			},
			PullRequest: struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				Head   struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				} `json:"head"`
				Base struct {
					Ref string `json:"ref"`
				} `json:"base"`
			}{
				Number: 42,
				Title:  "Review me",
				Head: struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				}{
					Ref: "feature/latest-head",
					SHA: "deadbeef",
				},
				Base: struct {
					Ref string `json:"ref"`
				}{
					Ref: "main",
				},
			},
			Repository: struct {
				FullName string `json:"full_name"`
				Owner    struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name          string `json:"name"`
				DefaultBranch string `json:"default_branch"`
				CloneURL      string `json:"clone_url"`
			}{
				Name:     "github-bridge",
				CloneURL: "https://github.com/openagent/github-bridge.git",
				Owner: struct {
					Login string `json:"login"`
				}{
					Login: "openagent",
				},
			},
			Sender: struct {
				Login string `json:"login"`
			}{
				Login: "alice",
			},
		},
	}

	task, err := handler.createTaskFromEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("createTaskFromEvent returned error: %v", err)
	}
	if task == nil {
		t.Fatalf("expected task to be created")
	}
	if task.Type != queue.TaskTypePRComment {
		t.Fatalf("expected PR comment task, got %q", task.Type)
	}
	if task.Branch != "feature/latest-head" {
		t.Fatalf("expected PR head branch, got %q", task.Branch)
	}
	if task.BaseBranch != "main" {
		t.Fatalf("expected PR base branch, got %q", task.BaseBranch)
	}
	if task.HeadSHA != "deadbeef" {
		t.Fatalf("expected PR head sha, got %q", task.HeadSHA)
	}
}

func TestCreateTaskFromPRDiscussionCommentWithoutLeadingMentionIsIgnored(t *testing.T) {
	t.Parallel()

	handler := newWebhookHandlerWithClient(
		config.GitHubConfig{WebhookSecret: "test-secret"},
		"@ogb-bot",
		queue.NewTaskQueue(1),
		fakePullRequestGetter{
			getPullRequest: func(ctx context.Context, owner, repo string, number int) (*ghapi.PullRequest, error) {
				t.Fatal("did not expect PR lookup when comment does not start with mention")
				return nil, nil
			},
		},
	)

	task, err := handler.createTaskFromEvent(context.Background(), &ghwebhook.WebhookEvent{
		Type:   "issue_comment",
		Action: "created",
		Payload: &ghwebhook.IssueCommentEvent{
			Action: "created",
			Comment: struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}{
				ID:   123,
				Body: "Please ping @ogb-bot later",
			},
			Issue: struct {
				Number      int    `json:"number"`
				Title       string `json:"title"`
				Body        string `json:"body"`
				State       string `json:"state"`
				PullRequest *struct {
					URL string `json:"url"`
				} `json:"pull_request,omitempty"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}{
				Number: 42,
				Title:  "PR title",
				PullRequest: &struct {
					URL string `json:"url"`
				}{
					URL: "https://api.github.com/repos/openagent/github-bridge/pulls/42",
				},
			},
			Repository: struct {
				FullName string `json:"full_name"`
				Owner    struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name          string `json:"name"`
				DefaultBranch string `json:"default_branch"`
				CloneURL      string `json:"clone_url"`
			}{
				Name: "github-bridge",
				Owner: struct {
					Login string `json:"login"`
				}{
					Login: "openagent",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("createTaskFromEvent returned error: %v", err)
	}
	if task != nil {
		t.Fatalf("expected task to be ignored, got %#v", task)
	}
}

func TestCreateTaskFromPRDiscussionCommentWithMentionUsesPRContext(t *testing.T) {
	t.Parallel()

	handler := newWebhookHandlerWithClient(
		config.GitHubConfig{WebhookSecret: "test-secret"},
		"@ogb-bot",
		queue.NewTaskQueue(1),
		fakePullRequestGetter{
			getPullRequest: func(ctx context.Context, owner, repo string, number int) (*ghapi.PullRequest, error) {
				if owner != "openagent" || repo != "github-bridge" || number != 42 {
					t.Fatalf("unexpected PR lookup: %s/%s#%d", owner, repo, number)
				}

				return &ghapi.PullRequest{
					User: &ghapi.User{
						Type: ghapi.Ptr("User"),
					},
					Head: &ghapi.PullRequestBranch{
						Ref: ghapi.Ptr("feature/latest-head"),
						SHA: ghapi.Ptr("deadbeef"),
					},
					Base: &ghapi.PullRequestBranch{
						Ref: ghapi.Ptr("main"),
					},
					Draft: ghapi.Ptr(false),
				}, nil
			},
		},
	)

	task, err := handler.createTaskFromEvent(context.Background(), &ghwebhook.WebhookEvent{
		Type:   "issue_comment",
		Action: "created",
		Payload: &ghwebhook.IssueCommentEvent{
			Action: "created",
			Comment: struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}{
				ID:   123,
				Body: "@ogb-bot please check this PR",
			},
			Issue: struct {
				Number      int    `json:"number"`
				Title       string `json:"title"`
				Body        string `json:"body"`
				State       string `json:"state"`
				PullRequest *struct {
					URL string `json:"url"`
				} `json:"pull_request,omitempty"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}{
				Number: 42,
				Title:  "PR title",
				Body:   "PR body",
				PullRequest: &struct {
					URL string `json:"url"`
				}{
					URL: "https://api.github.com/repos/openagent/github-bridge/pulls/42",
				},
			},
			Repository: struct {
				FullName string `json:"full_name"`
				Owner    struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name          string `json:"name"`
				DefaultBranch string `json:"default_branch"`
				CloneURL      string `json:"clone_url"`
			}{
				Name:          "github-bridge",
				DefaultBranch: "main",
				CloneURL:      "https://github.com/openagent/github-bridge.git",
				Owner: struct {
					Login string `json:"login"`
				}{
					Login: "openagent",
				},
			},
			Sender: struct {
				Login string `json:"login"`
			}{
				Login: "alice",
			},
		},
	})
	if err != nil {
		t.Fatalf("createTaskFromEvent returned error: %v", err)
	}
	if task == nil {
		t.Fatalf("expected task to be created")
	}
	if task.Type != queue.TaskTypePRComment {
		t.Fatalf("expected PR comment task, got %q", task.Type)
	}
	if task.Branch != "feature/latest-head" {
		t.Fatalf("expected PR head branch, got %q", task.Branch)
	}
	if task.BaseBranch != "main" {
		t.Fatalf("expected PR base branch, got %q", task.BaseBranch)
	}
	if task.HeadSHA != "deadbeef" {
		t.Fatalf("expected PR head sha, got %q", task.HeadSHA)
	}
}

func performWebhookRequest(t *testing.T, eventType string, payload []byte) (*httptest.ResponseRecorder, *queue.TaskQueue) {
	t.Helper()

	taskQueue := queue.NewTaskQueue(1)
	handler := NewWebhookHandler(
		config.GitHubConfig{WebhookSecret: "test-secret"},
		config.TriggerConfig{Prefix: "@ogb-bot"},
		taskQueue,
	)

	router := gin.New()
	router.POST("/webhook", handler.HandleWebhook)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", signPayload(payload, "test-secret"))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	return recorder, taskQueue
}

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
