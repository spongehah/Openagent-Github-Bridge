package service

import (
	"fmt"
	"strings"

	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

// PromptBuilder constructs prompts for the AI agent.
type PromptBuilder struct {
	triggerLabels []string
}

const githubProgressCommentSkillName = "github-progress-comment"
const issueToPRSkillName = "issue-to-pr"
const prReviewSkillName = "pr-review"

// NewPromptBuilder creates a new prompt builder.
func NewPromptBuilder(triggerLabels []string) *PromptBuilder {
	return &PromptBuilder{
		triggerLabels: triggerLabels,
	}
}

// Build creates a prompt from the task and session context.
func (pb *PromptBuilder) Build(task *queue.Task, sess *session.Session, isNew bool) string {
	// Check if this is a PR review task
	if task.Type == queue.TaskTypePRReview {
		return pb.buildPRReviewPrompt(task)
	}

	// Check if this is a label-triggered task
	if task.Action == "labeled" {
		matchedLabel := pb.getMatchedLabel(task.Labels)
		if matchedLabel != "" {
			return pb.buildLabelTriggeredPrompt(task, matchedLabel)
		}
	}

	return pb.buildStandardPrompt(task, sess, isNew)
}

// getMatchedLabel returns the first matched trigger label.
func (pb *PromptBuilder) getMatchedLabel(labels []string) string {
	for _, taskLabel := range labels {
		for _, triggerLabel := range pb.triggerLabels {
			if strings.EqualFold(taskLabel, triggerLabel) {
				return triggerLabel
			}
		}
	}
	return ""
}

// buildPRReviewPrompt creates a prompt for automatic PR review.
//
// 示例（PR opened 或 labeled 触发，task.Owner="openagent", task.Repo="bridge",
// task.Number=42, task.Branch="feat/login", task.BaseBranch="main",
// task.Sender="alice", task.HeadSHA="abc1234", task.Title="Add login flow",
// task.Body="Implements OAuth2 login."）：
//
//	# Pull Request Review Request
//
//	A new pull request has been opened and requires your review.
//
//	## PR Context
//
//	- **Repository:** openagent/bridge
//	- **PR Number:** #42
//	- **Branch:** `feat/login` -> `main`
//	- **Author:** @alice
//	- **Head SHA:** `abc1234`
//
//	---
//
//	## PR Title: Add login flow
//
//	### Description
//
//	Implements OAuth2 login.
//
//	---
//
//	## Review Goal
//
//	Produce a formal GitHub review for this PR. Focus on correctness, regressions,
//	security concerns, edge cases, and testing gaps.
func (pb *PromptBuilder) buildPRReviewPrompt(task *queue.Task) string {
	var sb strings.Builder

	sb.WriteString("# Pull Request Review Request\n\n")
	sb.WriteString("A new pull request has been opened and requires your review.\n\n")

	sb.WriteString("## PR Context\n\n")
	sb.WriteString(fmt.Sprintf("- **Repository:** %s/%s\n", task.Owner, task.Repo))
	sb.WriteString(fmt.Sprintf("- **PR Number:** #%d\n", task.Number))
	sb.WriteString(fmt.Sprintf("- **Branch:** `%s` -> `%s`\n", task.Branch, task.BaseBranch))
	sb.WriteString(fmt.Sprintf("- **Author:** @%s\n", task.Sender))
	if task.HeadSHA != "" {
		sb.WriteString(fmt.Sprintf("- **Head SHA:** `%s`\n", task.HeadSHA))
	}

	sb.WriteString("\n---\n\n")
	pb.writeSkillCoordination(&sb, task)

	sb.WriteString(fmt.Sprintf("## PR Title: %s\n\n", task.Title))
	if task.Body != "" {
		sb.WriteString("### Description\n\n")
		sb.WriteString(task.Body)
		sb.WriteString("\n\n")
	}

	sb.WriteString("---\n\n")

	sb.WriteString("## Review Goal\n\n")
	sb.WriteString("Produce a formal GitHub review for this PR. Focus on correctness, regressions, security concerns, edge cases, and testing gaps. Keep feedback actionable and file-specific when possible.\n")

	return sb.String()
}

// buildLabelTriggeredPrompt creates a prompt for label-triggered tasks (e.g., "ai-fix").
//
// 示例（Issue 被打上 "ai-fix" 标签触发，task.Owner="openagent", task.Repo="bridge",
// task.Number=7, task.Branch="main", task.Sender="bob",
// task.Labels=["bug","ai-fix"], task.Title="Null pointer in auth",
// task.IssueBody="Calling login() with nil user crashes the server."）：
//
//	# Automated Task Request
//
//	This issue has been labeled with `ai-fix`, indicating an automated fix is requested.
//
//	## Issue: Null pointer in auth
//
//	Calling login() with nil user crashes the server.
//
//	---
//
//	## Task Goal
//
//	Analyze the issue, implement the necessary fix, verify the change, and open a
//	pull request linked to this issue.
func (pb *PromptBuilder) buildLabelTriggeredPrompt(task *queue.Task, label string) string {
	var sb strings.Builder

	sb.WriteString("# Automated Task Request\n\n")
	sb.WriteString(fmt.Sprintf("This issue has been labeled with `%s`, indicating an automated fix is requested.\n\n", label))
	sb.WriteString(fmt.Sprintf("## Issue #%d: %s\n\n", task.Number, task.Title))
	if task.IssueBody != "" {
		sb.WriteString(task.IssueBody)
	} else if task.Body != "" {
		sb.WriteString(task.Body)
	}

	sb.WriteString("\n\n---\n\n")

	sb.WriteString("## Task Goal\n\n")
	sb.WriteString("Analyze the issue, implement the necessary fix, verify the change, and open a pull request linked to this issue.\n\n")
	sb.WriteString(fmt.Sprintf("Expected PR linkage: include `Fixes #%d` or `Closes #%d` in the PR description.\n", task.Number, task.Number))
	sb.WriteString("\n---\n\n")
	pb.writeSkillCoordination(&sb, task)

	return sb.String()
}

// buildStandardPrompt creates a standard prompt for comment/mention triggered tasks.
//
// 示例（task.Owner="openagent", task.Repo="bridge", task.Number=3, task.Branch="main",
// task.Sender="carol", task.Title="Add retry logic", task.IssueBody="We need retries.",
// task.CommentBody="@ogb-bot please implement exponential backoff."）：
//
//	# GitHub Task
//
//	- **Repository:** openagent/bridge
//	- **Branch:** main
//	- **Issue:** #3
//	- **Triggered by:** @carol
//	[- **Labels:** bug, help-wanted]
//
//	---
//
//	[## Add retry logic
//
//	We need retries.
//
//	---]
//
//	## Request
//
//	[@ogb-bot please implement exponential backoff.
//	 | Please analyze this issue and take appropriate action.]
//
//	[---
//
//	*This is a continuation of session `openagent/bridge/issue/3` with 2 previous interactions.*]
func (pb *PromptBuilder) buildStandardPrompt(task *queue.Task, sess *session.Session, isNew bool) string {
	var sb strings.Builder

	sb.WriteString("# GitHub Task\n\n")
	sb.WriteString(fmt.Sprintf("- **Repository:** %s/%s\n", task.Owner, task.Repo))
	sb.WriteString(fmt.Sprintf("- **Branch:** %s\n", task.Branch))

	if task.Type == queue.TaskTypePullRequest || task.Type == queue.TaskTypePRComment {
		sb.WriteString(fmt.Sprintf("- **Pull Request:** #%d\n", task.Number))
	} else {
		sb.WriteString(fmt.Sprintf("- **Issue:** #%d\n", task.Number))
	}

	sb.WriteString(fmt.Sprintf("- **Triggered by:** @%s\n", task.Sender))

	if len(task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("- **Labels:** %s\n", strings.Join(task.Labels, ", ")))
	}

	sb.WriteString("\n---\n\n")
	pb.writeSkillCoordination(&sb, task)

	// Add issue/PR details for new sessions
	if isNew && task.IssueBody != "" {
		sb.WriteString(fmt.Sprintf("## %s\n\n", task.Title))
		sb.WriteString(task.IssueBody)
		sb.WriteString("\n\n---\n\n")
	}

	// Add the current request
	if task.CommentBody != "" {
		sb.WriteString("## Request\n\n")
		sb.WriteString(task.CommentBody)
	} else if isNew {
		sb.WriteString("## Request\n\n")
		sb.WriteString("Please analyze this issue and take appropriate action.\n")
	}

	// Add session history summary if continuing a conversation
	if !isNew && len(sess.DispatchHistory) > 0 {
		sb.WriteString("\n\n---\n\n")
		sb.WriteString(fmt.Sprintf("*This is a continuation of session `%s` with %d previous interactions.*\n",
			sess.Key.String(), len(sess.DispatchHistory)))
	}

	return sb.String()
}

func (pb *PromptBuilder) writeSkillCoordination(sb *strings.Builder, task *queue.Task) {
	repoSlug := fmt.Sprintf("%s/%s", task.Owner, task.Repo)
	marker := fmt.Sprintf("<!-- openagent:progress-comment %s#%d -->", repoSlug, task.Number)
	outcomeLine := "- Response / branch / follow-up link"
	if task.Type == queue.TaskTypePRReview {
		outcomeLine = "- Review outcome / review link / follow-up"
	} else if pb.getFeatureSkill(task) == issueToPRSkillName {
		outcomeLine = "- PR / branch / follow-up link"
	}
	featureSkill := pb.getFeatureSkill(task)

	sb.WriteString("## Skill Order\n\n")
	if featureSkill != "" {
		sb.WriteString(fmt.Sprintf("1. **First:** call `skill %s` before substantial work so the temporary GitHub comment is created early.\n", githubProgressCommentSkillName))
		sb.WriteString(fmt.Sprintf("2. **Then:** call `skill %s` for the main workflow of this task.\n", featureSkill))
		sb.WriteString("3. **Fallback:** if a listed skill is unavailable, continue with the explicit instructions in this prompt.\n\n")
	} else {
		sb.WriteString("1. **Default:** for simple mentions, greetings, or brief clarifications, reply directly in the GitHub thread without creating a temporary progress comment.\n")
		sb.WriteString(fmt.Sprintf("2. **Only if needed:** if this turns into substantial or long-running work, call `skill %s` and keep updating that single progress comment.\n", githubProgressCommentSkillName))
		sb.WriteString("   Upgrade to progress-comment mode immediately if you need substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.\n")
		sb.WriteString("3. **Fallback:** if the skill is unavailable, continue with the explicit instructions in this prompt.\n\n")
	}

	sb.WriteString("## GitHub Interaction Protocol\n\n")
	sb.WriteString("- The user is interacting with you on GitHub, not in a direct chat session.\n")
	sb.WriteString("- Send every user-facing progress update, final summary, and blocker notice back through GitHub.\n")
	sb.WriteString("- Write all GitHub-facing user communication in Chinese.\n")
	if featureSkill != "" {
		sb.WriteString(fmt.Sprintf("- Prefer updating the single progress comment managed by `skill %s` instead of posting separate wrap-up comments.\n", githubProgressCommentSkillName))
	} else {
		sb.WriteString(fmt.Sprintf("- For quick replies, respond directly in the thread. Only use `skill %s` when the work is substantial enough to need incremental status updates.\n", githubProgressCommentSkillName))
		sb.WriteString("- Upgrade to progress-comment mode as soon as the task requires substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.\n")
	}
	if task.Type == queue.TaskTypePRReview {
		sb.WriteString("- For PR review tasks, keep status updates in the progress comment and submit the final verdict as a formal GitHub review.\n\n")
	} else {
		sb.WriteString("- Use another GitHub surface only when the task explicitly requires it, such as a PR body or PR metadata that links the implementation back to the issue.\n\n")
	}

	sb.WriteString("## Skill Coordination\n\n")
	if featureSkill != "" {
		sb.WriteString(fmt.Sprintf("- `skill %s` owns the progress comment lifecycle for `%s#%d`.\n", githubProgressCommentSkillName, repoSlug, task.Number))
		sb.WriteString(fmt.Sprintf("- `skill %s` owns the main task workflow after the progress comment exists.\n", featureSkill))
		sb.WriteString("- Do not create an extra progress or wrap-up comment later in the workflow.\n")
		sb.WriteString("- If later instructions mention summary, verification, or outcome, treat them as content for the existing progress comment unless they clearly refer to a PR body or formal review.\n")
		sb.WriteString(fmt.Sprintf("- Progress comment marker: `%s`\n", marker))
		sb.WriteString("- Progress comment completion fields: `Status`, `Summary`, `Verification`, `Outcome`\n")
		sb.WriteString(fmt.Sprintf("- Preferred `Outcome` line: `%s`\n\n", outcomeLine))
		sb.WriteString("---\n\n")
		return
	}
	sb.WriteString(fmt.Sprintf("- Use `skill %s` only when the work becomes long-running enough to justify a mutable progress comment.\n", githubProgressCommentSkillName))
	sb.WriteString("- Do not create a temporary progress comment for simple acknowledgements, greetings, or brief Q&A.\n")
	sb.WriteString("- Upgrade immediately when the work requires substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.\n")
	sb.WriteString("- If you decide a progress comment is necessary, reuse one marked with the marker below and keep summary, verification, and outcome in that single comment.\n")
	sb.WriteString("- Once a session is upgraded into progress-comment mode, keep reusing that same progress comment for subsequent updates in the session.\n")
	sb.WriteString(fmt.Sprintf("- Progress comment marker: `%s`\n", marker))
	sb.WriteString("- Progress comment completion fields: `Status`, `Summary`, `Verification`, `Outcome`\n")
	sb.WriteString(fmt.Sprintf("- Preferred `Outcome` line when a progress comment is used: `%s`\n\n", outcomeLine))
	sb.WriteString("---\n\n")
}

func (pb *PromptBuilder) getFeatureSkill(task *queue.Task) string {
	switch task.Type {
	case queue.TaskTypePRReview:
		return prReviewSkillName
	default:
		if task.Action == "labeled" && pb.getMatchedLabel(task.Labels) != "" {
			return issueToPRSkillName
		}
		return ""
	}
}
