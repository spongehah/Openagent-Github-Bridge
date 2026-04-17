package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openagent/github-bridge/internal/config"
	"github.com/openagent/github-bridge/internal/queue"
)

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

func performWebhookRequest(t *testing.T, eventType string, payload []byte) (*httptest.ResponseRecorder, *queue.TaskQueue) {
	t.Helper()

	taskQueue := queue.NewTaskQueue(1)
	handler := NewWebhookHandler(config.GitHubConfig{WebhookSecret: "test-secret"}, taskQueue)

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
