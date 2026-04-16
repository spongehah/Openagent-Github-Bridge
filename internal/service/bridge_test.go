package service

import (
	"testing"

	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

func TestGetSessionKeyTreatsPRReviewAsPullRequest(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{}
	key := svc.getSessionKey(&queue.Task{
		Type:   queue.TaskTypePRReview,
		Owner:  "openagent",
		Repo:   "github-bridge",
		Number: 99,
	})

	if key.Type != session.SessionTypePullRequest {
		t.Fatalf("expected PR session type, got %q", key.Type)
	}
}
