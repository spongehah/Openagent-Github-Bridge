package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestAddIssueWorktreePrefersRemoteRef(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))

		switch strings.Join(args, " ") {
		case "rev-parse --verify origin/main^{commit}":
			return "commit\n", nil
		case "worktree add -B issue-42 /tmp/worktree origin/main":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected git call: %s", strings.Join(args, " "))
		}
	}

	if err := addIssueWorktree(context.Background(), "/repo", "/tmp/worktree", "issue-42", "main", "origin", run); err != nil {
		t.Fatalf("addIssueWorktree returned error: %v", err)
	}

	want := []string{
		"rev-parse --verify origin/main^{commit}",
		"worktree add -B issue-42 /tmp/worktree origin/main",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestAddIssueWorktreeFallsBackToSyncedLocalBranch(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)

		switch call {
		case "rev-parse --verify origin/main^{commit}":
			return "commit\n", nil
		case "worktree add -B issue-42 /tmp/worktree origin/main":
			return "", fmt.Errorf("remote ref cannot be checked out directly")
		case "fetch origin main:refs/heads/main":
			return "", nil
		case "rev-parse --verify main^{commit}":
			return "commit\n", nil
		case "worktree add -B issue-42 /tmp/worktree main":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected git call: %s", call)
		}
	}

	if err := addIssueWorktree(context.Background(), "/repo", "/tmp/worktree", "issue-42", "main", "origin", run); err != nil {
		t.Fatalf("addIssueWorktree returned error: %v", err)
	}

	want := []string{
		"rev-parse --verify origin/main^{commit}",
		"worktree add -B issue-42 /tmp/worktree origin/main",
		"fetch origin main:refs/heads/main",
		"rev-parse --verify main^{commit}",
		"worktree add -B issue-42 /tmp/worktree main",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}
