package agent

import "testing"

func TestBuildWorktreeCreateRequestForIssue(t *testing.T) {
	t.Parallel()

	args, err := buildWorktreeCreateRequest(TaskContext{
		RepoOwner:     "openagent",
		RepoName:      "github-bridge",
		EventType:     "issue",
		IssueNumber:   42,
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("buildWorktreeCreateRequest returned error: %v", err)
	}

	if args.Kind != "issue" {
		t.Fatalf("expected issue kind, got %q", args.Kind)
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

func TestBuildWorktreeCreateRequestForPR(t *testing.T) {
	t.Parallel()

	args, err := buildWorktreeCreateRequest(TaskContext{
		RepoOwner:   "openagent",
		RepoName:    "github-bridge",
		EventType:   "pr_review",
		IssueNumber: 128,
		Branch:      "feature/smaller-worktree",
		HeadSHA:     "abc123",
	})
	if err != nil {
		t.Fatalf("buildWorktreeCreateRequest returned error: %v", err)
	}

	if args.Kind != "pr_review" {
		t.Fatalf("expected pr_review kind, got %q", args.Kind)
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
