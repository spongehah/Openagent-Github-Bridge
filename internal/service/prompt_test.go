package service

import (
	"strings"
	"testing"

	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

func TestPromptBuilderIssuePromptIncludesSkillCoordination(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"})
	sess := session.NewSession(session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 42))

	prompt := builder.Build(&queue.Task{
		Type:      queue.TaskTypeIssue,
		Owner:     "openagent",
		Repo:      "github-bridge",
		Number:    42,
		Branch:    "main",
		Title:     "Fix bridge flow",
		IssueBody: "Body",
		Sender:    "alice",
	}, sess, true)

	if !strings.Contains(prompt, "## Skill Coordination") {
		t.Fatalf("expected skill coordination in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, githubProgressCommentSkillName) {
		t.Fatalf("expected progress comment skill in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "1. **First:** call `skill github-progress-comment`") {
		t.Fatalf("expected github-progress-comment as first skill step in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "2. **Then:** call `skill issue-to-pr`") {
		t.Fatalf("expected issue-to-pr as second skill step in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "<!-- openagent:progress-comment openagent/github-bridge#42 -->") {
		t.Fatalf("expected progress comment marker in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not create an extra progress or wrap-up comment later in the workflow.") {
		t.Fatalf("expected duplicate-comment guardrail in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## GitHub Interaction Protocol") {
		t.Fatalf("expected GitHub interaction protocol section in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Write all GitHub-facing user communication in Chinese.") {
		t.Fatalf("expected Chinese GitHub communication guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "The user is interacting with you on GitHub, not in a direct chat session.") {
		t.Fatalf("expected GitHub-only interaction guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Send every user-facing progress update, final summary, and blocker notice back through GitHub.") {
		t.Fatalf("expected GitHub feedback channel guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Prefer updating the single progress comment managed by `skill github-progress-comment` instead of posting separate wrap-up comments.") {
		t.Fatalf("expected progress comment preference in prompt: %q", prompt)
	}
}

func TestPromptBuilderLabelTriggeredPromptUsesShorterContextAndTaskFirstOrder(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"})
	sess := session.NewSession(session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 3))

	prompt := builder.Build(&queue.Task{
		Type:      queue.TaskTypeIssue,
		Action:    "labeled",
		Owner:     "openagent",
		Repo:      "github-bridge",
		Number:    3,
		Branch:    "main",
		Title:     "Trim the prompt",
		IssueBody: "Prompt body",
		Sender:    "alice",
		Labels:    []string{"bug", "ai-fix"},
	}, sess, true)

	if strings.Contains(prompt, "## Task Context") {
		t.Fatalf("did not expect verbose task context section in label prompt: %q", prompt)
	}
	if strings.Contains(prompt, "**Default Branch:**") {
		t.Fatalf("did not expect default branch in label prompt: %q", prompt)
	}
	if strings.Contains(prompt, "**Labels:**") || strings.Contains(prompt, "- **Labels:**") {
		t.Fatalf("did not expect labels list in label prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## Issue #3: Trim the prompt") {
		t.Fatalf("expected issue heading with issue number in label prompt: %q", prompt)
	}

	issueIdx := strings.Index(prompt, "## Issue #3: Trim the prompt")
	skillIdx := strings.Index(prompt, "## Skill Order")
	if issueIdx == -1 || skillIdx == -1 || issueIdx > skillIdx {
		t.Fatalf("expected issue details before skill coordination in label prompt: %q", prompt)
	}
}

func TestPromptBuilderPRReviewPromptIncludesReviewOutcomeSection(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"})

	prompt := builder.Build(&queue.Task{
		Type:       queue.TaskTypePRReview,
		Owner:      "openagent",
		Repo:       "github-bridge",
		Number:     7,
		Branch:     "feature/test",
		BaseBranch: "main",
		Title:      "Add review flow",
		Body:       "PR body",
		Sender:     "bob",
	}, session.NewSession(session.NewSessionKey("openagent", "github-bridge", session.SessionTypePullRequest, 7)), true)

	if !strings.Contains(prompt, "Review outcome / review link / follow-up") {
		t.Fatalf("expected PR review outcome guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "2. **Then:** call `skill pr-review`") {
		t.Fatalf("expected pr-review as second skill step in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "`skill pr-review` owns the main task workflow after the progress comment exists.") {
		t.Fatalf("expected pr-review ownership guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "For PR review tasks, keep status updates in the progress comment and submit the final verdict as a formal GitHub review.") {
		t.Fatalf("expected formal review exception in prompt: %q", prompt)
	}
}
