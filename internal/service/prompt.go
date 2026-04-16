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
//	## Review Instructions
//
//	Please review this pull request:
//
//	1. **Examine the Changes:** Review all modified files and understand the changes
//	2. **Check Code Quality:** Look for bugs, code smells, and potential improvements
//	3. **Verify Best Practices:** Ensure the code follows project conventions
//	4. **Security Review:** Check for security vulnerabilities or sensitive data exposure
//	5. **Provide Feedback:** Leave constructive comments on specific lines if needed
//	6. **Submit Review:** Approve, request changes, or comment based on your findings
//
//	Focus on being helpful and constructive in your feedback.
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

	sb.WriteString(fmt.Sprintf("## PR Title: %s\n\n", task.Title))
	if task.Body != "" {
		sb.WriteString("### Description\n\n")
		sb.WriteString(task.Body)
		sb.WriteString("\n\n")
	}

	sb.WriteString("---\n\n")

	sb.WriteString("## Review Instructions\n\n")
	sb.WriteString("Please review this pull request:\n\n")
	sb.WriteString("1. **Examine the Changes:** Review all modified files and understand the changes\n")
	sb.WriteString("2. **Check Code Quality:** Look for bugs, code smells, and potential improvements\n")
	sb.WriteString("3. **Verify Best Practices:** Ensure the code follows project conventions\n")
	sb.WriteString("4. **Security Review:** Check for security vulnerabilities or sensitive data exposure\n")
	sb.WriteString("5. **Provide Feedback:** Leave constructive comments on specific lines if needed\n")
	sb.WriteString("6. **Submit Review:** Approve, request changes, or comment based on your findings\n\n")
	sb.WriteString("Focus on being helpful and constructive in your feedback.\n")

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
//	## Task Context
//
//	- **Repository:** openagent/bridge
//	- **Default Branch:** main
//	- **Issue Number:** #7
//	- **Triggered by:** @bob (via label)
//	- **Labels:** bug, ai-fix
//
//	---
//
//	## Issue: Null pointer in auth
//
//	Calling login() with nil user crashes the server.
//
//	---
//
//	## Instructions
//
//	Please analyze this issue and implement a fix:
//
//	1. **Understand the Problem:** Read the issue description carefully
//	2. **Plan the Solution:** Create a plan for the fix
//	3. **Implement the Fix:** Make the necessary code changes
//	4. **Create a Pull Request:** Submit your changes as a PR that references this issue
//
//	When creating the PR, include `Fixes #7` or `Closes #7` in the description to link it to this issue.
func (pb *PromptBuilder) buildLabelTriggeredPrompt(task *queue.Task, label string) string {
	var sb strings.Builder

	sb.WriteString("# Automated Task Request\n\n")
	sb.WriteString(fmt.Sprintf("This issue has been labeled with `%s`, indicating an automated fix is requested.\n\n", label))

	sb.WriteString("## Task Context\n\n")
	sb.WriteString(fmt.Sprintf("- **Repository:** %s/%s\n", task.Owner, task.Repo))
	sb.WriteString(fmt.Sprintf("- **Default Branch:** %s\n", task.Branch))
	sb.WriteString(fmt.Sprintf("- **Issue Number:** #%d\n", task.Number))
	sb.WriteString(fmt.Sprintf("- **Triggered by:** @%s (via label)\n", task.Sender))

	if len(task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("- **Labels:** %s\n", strings.Join(task.Labels, ", ")))
	}

	sb.WriteString("\n---\n\n")

	sb.WriteString(fmt.Sprintf("## Issue: %s\n\n", task.Title))
	if task.IssueBody != "" {
		sb.WriteString(task.IssueBody)
	} else if task.Body != "" {
		sb.WriteString(task.Body)
	}

	sb.WriteString("\n\n---\n\n")

	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Please analyze this issue and implement a fix:\n\n")
	sb.WriteString("1. **Understand the Problem:** Read the issue description carefully\n")
	sb.WriteString("2. **Plan the Solution:** Create a plan for the fix\n")
	sb.WriteString("3. **Implement the Fix:** Make the necessary code changes\n")
	sb.WriteString("4. **Create a Pull Request:** Submit your changes as a PR that references this issue\n\n")
	sb.WriteString(fmt.Sprintf("When creating the PR, include `Fixes #%d` or `Closes #%d` in the description to link it to this issue.\n", task.Number, task.Number))

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
