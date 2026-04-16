package queue

import "testing"

func TestSessionKeyFromTaskTreatsPRReviewAsPR(t *testing.T) {
	t.Parallel()

	key := SessionKeyFromTask(&Task{
		Type:   TaskTypePRReview,
		Owner:  "openagent",
		Repo:   "github-bridge",
		Number: 77,
	})

	if key != "openagent/github-bridge/pr/77" {
		t.Fatalf("unexpected session key: %s", key)
	}
}
