package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCloneWorkspaceUsesConfiguredRemoteName(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, cwd string, args ...string) (string, error) {
		calls = append(calls, fmt.Sprintf("%s :: %s", cwd, strings.Join(args, " ")))
		if cwd != "/tmp" {
			t.Fatalf("expected clone to run from parent directory, got %s", cwd)
		}
		return "", nil
	}

	if err := cloneWorkspace(context.Background(), "git@github.com:openagent/github-bridge.git", "/tmp/workspace", "origin", run); err != nil {
		t.Fatalf("cloneWorkspace returned error: %v", err)
	}

	want := []string{
		"/tmp :: clone --origin origin git@github.com:openagent/github-bridge.git /tmp/workspace",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestEnsureWorkspaceRemoteUpdatesOriginWhenDifferent(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, cwd string, args ...string) (string, error) {
		call := fmt.Sprintf("%s :: %s", cwd, strings.Join(args, " "))
		calls = append(calls, call)

		switch call {
		case "/tmp/workspace :: remote get-url origin":
			return "https://github.com/openagent/github-bridge.git\n", nil
		case "/tmp/workspace :: remote set-url origin git@github.com:openagent/github-bridge.git":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected git call: %s", call)
		}
	}

	if err := ensureWorkspaceRemote(context.Background(), "/tmp/workspace", "git@github.com:openagent/github-bridge.git", "origin", run); err != nil {
		t.Fatalf("ensureWorkspaceRemote returned error: %v", err)
	}

	want := []string{
		"/tmp/workspace :: remote get-url origin",
		"/tmp/workspace :: remote set-url origin git@github.com:openagent/github-bridge.git",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestCreateIssueWorkspaceChecksOutRemoteBranch(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, cwd string, args ...string) (string, error) {
		call := fmt.Sprintf("%s :: %s", cwd, strings.Join(args, " "))
		calls = append(calls, call)

		switch call {
		case "/tmp/workspace :: fetch --all --prune":
			return "", nil
		case "/tmp/workspace :: rev-parse --verify origin/main^{commit}":
			return "commit\n", nil
		case "/tmp/workspace :: checkout -B issue-42 origin/main":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected git call: %s", call)
		}
	}

	if err := createIssueWorkspace(context.Background(), "/tmp/workspace", "issue-42", "main", "origin", run); err != nil {
		t.Fatalf("createIssueWorkspace returned error: %v", err)
	}

	want := []string{
		"/tmp/workspace :: fetch --all --prune",
		"/tmp/workspace :: rev-parse --verify origin/main^{commit}",
		"/tmp/workspace :: checkout -B issue-42 origin/main",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestCreateOrRefreshPRWorkspaceResetsToLatestHead(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, cwd string, args ...string) (string, error) {
		call := fmt.Sprintf("%s :: %s", cwd, strings.Join(args, " "))
		calls = append(calls, call)

		switch call {
		case "/tmp/workspace :: fetch --all --prune":
			return "", nil
		case "/tmp/workspace :: rev-parse --verify deadbeef^{commit}":
			return "deadbeef\n", nil
		case "/tmp/workspace :: reset --hard":
			return "", nil
		case "/tmp/workspace :: clean -fdx":
			return "", nil
		case "/tmp/workspace :: checkout -B pr-42 deadbeef":
			return "", nil
		case "/tmp/workspace :: reset --hard deadbeef":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected git call: %s", call)
		}
	}

	if err := createOrRefreshPRWorkspace(context.Background(), "/tmp/workspace", "pr-42", "feature/latest-head", "deadbeef", "origin", run); err != nil {
		t.Fatalf("createOrRefreshPRWorkspace returned error: %v", err)
	}

	want := []string{
		"/tmp/workspace :: fetch --all --prune",
		"/tmp/workspace :: rev-parse --verify deadbeef^{commit}",
		"/tmp/workspace :: reset --hard",
		"/tmp/workspace :: clean -fdx",
		"/tmp/workspace :: checkout -B pr-42 deadbeef",
		"/tmp/workspace :: reset --hard deadbeef",
		"/tmp/workspace :: clean -fdx",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected git calls:\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestRemoveManagedWorkspaceRejectsUnmanagedPath(t *testing.T) {
	t.Parallel()

	err := removeManagedWorkspace("/tmp/managed", "/tmp/other/workspace")
	if err == nil || !strings.Contains(err.Error(), "refusing to remove unmanaged workspace path") {
		t.Fatalf("expected unmanaged workspace error, got %v", err)
	}
}
