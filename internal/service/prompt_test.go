package service

import (
	"strings"
	"testing"

	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

func TestPromptBuilderIssuePromptUsesLightweightGitHubCoordination(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"})
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

	if !strings.HasPrefix(prompt, "# Mandatory Execution Requirements\n\n") {
		t.Fatalf("expected execution requirements at the top of prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## Skill Coordination") {
		t.Fatalf("expected skill coordination in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, githubProgressCommentSkillName) {
		t.Fatalf("expected progress comment skill in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "1. **Default:** for simple mentions, greetings, or brief clarifications, reply directly in the GitHub thread without creating a temporary progress comment.") {
		t.Fatalf("expected direct-reply guidance for ordinary issue prompts: %q", prompt)
	}
	if !strings.Contains(prompt, "2. **Only if needed:** if this turns into substantial or long-running work, call `skill github-progress-comment`") {
		t.Fatalf("expected conditional progress-comment guidance for ordinary issue prompts: %q", prompt)
	}
	if !strings.Contains(prompt, "Upgrade to progress-comment mode immediately if you need substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.") {
		t.Fatalf("expected explicit upgrade conditions for ordinary issue prompts: %q", prompt)
	}
	if strings.Contains(prompt, "call `skill issue-to-pr`") {
		t.Fatalf("did not expect issue-to-pr guidance for ordinary issue prompts: %q", prompt)
	}
	if !strings.Contains(prompt, "<!-- openagent:progress-comment openagent/github-bridge#42 -->") {
		t.Fatalf("expected progress comment marker in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not create a temporary progress comment for simple acknowledgements, greetings, or brief Q&A.") {
		t.Fatalf("expected simple-reply guardrail in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## GitHub Interaction Protocol") {
		t.Fatalf("expected GitHub interaction protocol section in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## Repository Execution Guardrails") {
		t.Fatalf("expected repository execution guardrails in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "The worktree is already prepared for this task.") {
		t.Fatalf("expected prepared-worktree guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Stay on the current local branch `issue-42`") {
		t.Fatalf("expected issue worktree branch guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "push the current `HEAD` directly to `issue-42` using `HEAD:issue-42`") {
		t.Fatalf("expected issue push-target guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Write all GitHub-facing user communication in Chinese.") {
		t.Fatalf("expected Chinese GitHub communication guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "You must post at least one user-visible GitHub comment (`gh issue comment` or `gh pr comment`) before finishing the task; never complete the task silently.") {
		t.Fatalf("expected explicit minimum GitHub comment requirement in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "The user is interacting with you on GitHub, not in a direct chat session.") {
		t.Fatalf("expected GitHub-only interaction guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Send every user-facing progress update, final summary, and blocker notice back through GitHub.") {
		t.Fatalf("expected GitHub feedback channel guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "For quick replies, respond directly in the thread. Only use `skill github-progress-comment` when the work is substantial enough to need incremental status updates.") {
		t.Fatalf("expected lightweight GitHub interaction guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Upgrade to progress-comment mode as soon as the task requires substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.") {
		t.Fatalf("expected explicit progress-mode upgrade guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Once a session is upgraded into progress-comment mode, keep reusing that same progress comment for subsequent updates in the session.") {
		t.Fatalf("expected progress-comment reuse guidance after upgrade in prompt: %q", prompt)
	}
	reqIdx := strings.Index(prompt, "## Request")
	issueIdx := strings.Index(prompt, "## Issue #42: Fix bridge flow")
	skillIdx := strings.Index(prompt, "## Skill Order")
	if reqIdx == -1 || issueIdx == -1 || skillIdx == -1 || issueIdx < skillIdx || reqIdx < issueIdx {
		t.Fatalf("expected issue context and request to be appended after constraints and skill coordination: %q", prompt)
	}
}

func TestPromptBuilderLabelTriggeredPromptUsesShorterContextAndTaskFirstOrder(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"})
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
	if issueIdx == -1 || skillIdx == -1 || issueIdx < skillIdx {
		t.Fatalf("expected issue details after skill coordination in label prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "1. **First:** call `skill github-progress-comment`") {
		t.Fatalf("expected github-progress-comment as first skill step in label prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "2. **Then:** call `skill issue-to-pr`") {
		t.Fatalf("expected issue-to-pr as second skill step in label prompt: %q", prompt)
	}
}

func TestPromptBuilderPRReviewPromptIncludesReviewOutcomeSection(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"})

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
	if !strings.Contains(prompt, "Stay on the current local branch `pr-7`") {
		t.Fatalf("expected prepared PR worktree guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Compare it against `openagent/github-bridge/main` by default unless the task says otherwise.") {
		t.Fatalf("expected PR review baseline guidance in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "push the current `HEAD` directly to the PR branch `feature/test` using `HEAD:feature/test`") {
		t.Fatalf("expected PR push-target guidance in prompt: %q", prompt)
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
	ctxIdx := strings.Index(prompt, "## PR Context")
	goalIdx := strings.Index(prompt, "## Review Goal")
	skillIdx := strings.Index(prompt, "## Skill Order")
	if ctxIdx == -1 || goalIdx == -1 || skillIdx == -1 || ctxIdx < goalIdx || goalIdx < skillIdx {
		t.Fatalf("expected PR context to be appended after top-level constraints and review goal: %q", prompt)
	}
}

func TestPromptBuilderPlanLabelTriggeredPromptUsesGhIssuePlan(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"})
	sess := session.NewSession(session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 5))

	prompt := builder.Build(&queue.Task{
		Type:      queue.TaskTypeIssue,
		Action:    "labeled",
		Owner:     "openagent",
		Repo:      "github-bridge",
		Number:    5,
		Branch:    "main",
		Title:     "Plan only",
		IssueBody: "Need a design first",
		Sender:    "alice",
		Labels:    []string{"ai-plan"},
	}, sess, true)

	if !strings.Contains(prompt, "2. **Then:** call `skill issue-plan`") {
		t.Fatalf("expected issue-plan guidance in plan prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not implement code, modify files, or open a pull request in this run.") {
		t.Fatalf("expected plan-only restriction in plan prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Preferred `Outcome` line: `- Plan / open questions / follow-up link`") {
		t.Fatalf("expected plan outcome guidance in plan prompt: %q", prompt)
	}
}

func TestPromptBuilderGoCommentTriggeredPromptUsesIssueToPR(t *testing.T) {
	t.Parallel()

	builder := NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"})
	sess := session.NewSession(session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 6))

	prompt := builder.Build(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		Owner:       "openagent",
		Repo:        "github-bridge",
		Number:      6,
		Branch:      "main",
		Title:       "Code from slash command",
		IssueBody:   "Issue body",
		CommentBody: "/go keep the response backward compatible",
		Sender:      "alice",
	}, sess, true)

	if !strings.Contains(prompt, "2. **Then:** call `skill issue-to-pr`") {
		t.Fatalf("expected issue-to-pr guidance in slash prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "## User Instruction\n\nkeep the response backward compatible") {
		t.Fatalf("expected stripped user instruction in slash prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Expected PR linkage: include `Fixes #6` or `Closes #6` in the PR description.") {
		t.Fatalf("expected PR linkage guidance in slash prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "push the current `HEAD` directly to `issue-6` using `HEAD:issue-6`") {
		t.Fatalf("expected issue push-target guidance in slash prompt: %q", prompt)
	}
	reqIdx := strings.Index(prompt, "## User Instruction")
	issueIdx := strings.Index(prompt, "## Issue #6: Code from slash command")
	skillIdx := strings.Index(prompt, "## Skill Order")
	if reqIdx == -1 || issueIdx == -1 || skillIdx == -1 || issueIdx < skillIdx || reqIdx < issueIdx {
		t.Fatalf("expected issue context and request to be appended at the end of slash prompt: %q", prompt)
	}
}
