package service

import (
	"fmt"
	"strings"

	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

// PromptBuilder constructs prompts for the AI agent.
type PromptBuilder struct {
	triggerLabels   []string
	planLabels      []string
	commentCommands []string
}

const githubProgressCommentSkillName = "github-progress-comment"
const issueToPRSkillName = "issue-to-pr"
const issuePlanSkillName = "issue-plan"
const prReviewSkillName = "pr-review"

// NewPromptBuilder creates a new prompt builder.
func NewPromptBuilder(triggerLabels, planLabels, commentCommands []string) *PromptBuilder {
	return &PromptBuilder{
		triggerLabels:   triggerLabels,
		planLabels:      planLabels,
		commentCommands: commentCommands,
	}
}

// Build creates a prompt from the task and session context.
func (pb *PromptBuilder) Build(task *queue.Task, sess *session.Session, isNew bool) string {
	// Check if this is a PR review task
	if task.Type == queue.TaskTypePRReview {
		return pb.buildPRReviewPrompt(task)
	}

	if task.Action == "labeled" {
		matchedPlanLabel := pb.getMatchedPlanLabel(task.Labels)
		if matchedPlanLabel != "" {
			return pb.buildPlanLabelTriggeredPrompt(task, matchedPlanLabel)
		}

		matchedLabel := pb.getMatchedLabel(task.Labels)
		if matchedLabel != "" {
			return pb.buildLabelTriggeredPrompt(task, matchedLabel)
		}
	}

	if task.Type == queue.TaskTypeIssueComment {
		command, instruction := matchSlashCommand(task.CommentBody, pb.commentCommands)
		if command != "" {
			return pb.buildSlashCommandTriggeredPrompt(task, command, instruction)
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

// getMatchedPlanLabel returns the first matched planning label.
func (pb *PromptBuilder) getMatchedPlanLabel(labels []string) string {
	for _, taskLabel := range labels {
		for _, triggerLabel := range pb.planLabels {
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

	pb.writePromptPrelude(&sb, task)

	sb.WriteString("# Pull Request Review Request\n\n")
	sb.WriteString("A new pull request has been opened and requires your review.\n\n")

	sb.WriteString("## Review Goal\n\n")
	sb.WriteString("Produce a formal GitHub review for this PR. Focus on correctness, regressions, security concerns, edge cases, and testing gaps. Keep feedback actionable and file-specific when possible.\n")
	sb.WriteString("\n---\n\n")
	pb.writePRContext(&sb, task)

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

	pb.writePromptPrelude(&sb, task)

	sb.WriteString("# Automated Task Request\n\n")
	sb.WriteString(fmt.Sprintf("This issue has been labeled with `%s`, indicating an automated fix is requested.\n\n", label))
	sb.WriteString("## Task Goal\n\n")
	sb.WriteString("Analyze the issue, implement the necessary fix, verify the change, and open a pull request linked to this issue.\n\n")
	sb.WriteString(fmt.Sprintf("Expected PR linkage: include `Fixes #%d` or `Closes #%d` in the PR description.\n", task.Number, task.Number))
	sb.WriteString("\n---\n\n")
	pb.writeIssueContext(&sb, task)

	return sb.String()
}

func (pb *PromptBuilder) buildPlanLabelTriggeredPrompt(task *queue.Task, label string) string {
	var sb strings.Builder

	pb.writePromptPrelude(&sb, task)

	sb.WriteString("# Planning Task Request\n\n")
	sb.WriteString(fmt.Sprintf("This issue has been labeled with `%s`, indicating that an implementation plan is requested before coding.\n\n", label))
	sb.WriteString("## Task Goal\n\n")
	sb.WriteString("Analyze the issue against the current repository and write back an actionable implementation plan to the GitHub issue.\n")
	sb.WriteString("Do not implement code, modify files, or open a pull request in this run.\n")
	sb.WriteString("\n---\n\n")
	pb.writeIssueContext(&sb, task)

	return sb.String()
}

func (pb *PromptBuilder) buildSlashCommandTriggeredPrompt(task *queue.Task, command, instruction string) string {
	var sb strings.Builder

	pb.writePromptPrelude(&sb, task)

	sb.WriteString("# Implementation Task Request\n\n")
	sb.WriteString(fmt.Sprintf("A user requested issue implementation by commenting `%s`.\n\n", command))
	sb.WriteString("## Task Goal\n\n")
	sb.WriteString("Review the issue context and existing discussion, implement the requested change, verify it, and open a pull request linked to this issue.\n")
	sb.WriteString(fmt.Sprintf("Expected PR linkage: include `Fixes #%d` or `Closes #%d` in the PR description.\n", task.Number, task.Number))
	sb.WriteString("\n---\n\n")
	pb.writeIssueContext(&sb, task)
	pb.writeRequestSection(&sb, "## User Instruction", instruction, "No additional instruction was provided after the slash command.")

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

	pb.writePromptPrelude(&sb, task)

	sb.WriteString("# GitHub Task\n\n")
	sb.WriteString("Handle the GitHub request using the current repository context and session history.\n")
	sb.WriteString("\n---\n\n")

	// Add issue/PR details for new sessions
	if isNew {
		if task.Type == queue.TaskTypePullRequest || task.Type == queue.TaskTypePRComment {
			pb.writePRContext(&sb, task)
		} else if task.IssueBody != "" || task.Body != "" || task.Title != "" {
			pb.writeIssueContext(&sb, task)
		}
	}

	// Add the current request
	if task.CommentBody != "" {
		pb.writeRequestSection(&sb, "## Request", task.CommentBody, "")
	} else if isNew {
		pb.writeRequestSection(&sb, "## Request", "Please analyze this issue and take appropriate action.\n", "")
	}

	// Add session history summary if continuing a conversation
	if !isNew && len(sess.DispatchHistory) > 0 {
		sb.WriteString("\n\n---\n\n")
		sb.WriteString(fmt.Sprintf("*This is a continuation of session `%s` with %d previous interactions.*\n",
			sess.Key.String(), len(sess.DispatchHistory)))
	}

	return sb.String()
}

func (pb *PromptBuilder) writePRContext(sb *strings.Builder, task *queue.Task) {
	sb.WriteString("## PR Context\n\n")
	sb.WriteString(fmt.Sprintf("- **Repository:** %s/%s\n", task.Owner, task.Repo))
	sb.WriteString(fmt.Sprintf("- **PR Number:** #%d\n", task.Number))
	sb.WriteString(fmt.Sprintf("- **Branch:** `%s` -> `%s`\n", task.Branch, task.BaseBranch))
	sb.WriteString(fmt.Sprintf("- **Author:** @%s\n", task.Sender))
	if task.HeadSHA != "" {
		sb.WriteString(fmt.Sprintf("- **Head SHA:** `%s`\n", task.HeadSHA))
	}
	if len(task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("- **Labels:** %s\n", strings.Join(task.Labels, ", ")))
	}
	sb.WriteString("\n\n")
	if task.Title != "" {
		sb.WriteString(fmt.Sprintf("## PR Title: %s\n\n", task.Title))
	}
	if task.Body != "" {
		sb.WriteString("### Description\n\n")
		sb.WriteString(task.Body)
		sb.WriteString("\n\n")
	}
	sb.WriteString("---\n\n")
}

func (pb *PromptBuilder) writeIssueContext(sb *strings.Builder, task *queue.Task) {
	sb.WriteString(fmt.Sprintf("## Issue #%d: %s\n\n", task.Number, task.Title))
	if task.IssueBody != "" {
		sb.WriteString(task.IssueBody)
	} else if task.Body != "" {
		sb.WriteString(task.Body)
	}
	sb.WriteString("\n\n---\n\n")
}

func (pb *PromptBuilder) writeRequestSection(sb *strings.Builder, heading, content, fallback string) {
	sb.WriteString(heading)
	sb.WriteString("\n\n")
	if content != "" {
		sb.WriteString(content)
	} else if fallback != "" {
		sb.WriteString(fallback)
	}
	sb.WriteString("\n\n---\n\n")
}

func (pb *PromptBuilder) writePromptPrelude(sb *strings.Builder, task *queue.Task) {
	pb.writeExecutionRequirements(sb)
	pb.writeRepositoryExecutionGuardrails(sb, task)
	pb.writeSkillCoordination(sb, task)
}

func (pb *PromptBuilder) writeExecutionRequirements(sb *strings.Builder) {
	sb.WriteString("# Mandatory Execution Requirements\n\n")
	sb.WriteString("- Follow the repository instructions, including `AGENTS.md`.\n")
	sb.WriteString("- Treat the task prompt below as the source of truth for task workflow, skill order, and GitHub-side coordination.\n")
	sb.WriteString("- When instructions overlap, prefer the more specific task prompt or called skill, and do not repeat the same GitHub action twice.\n")
	sb.WriteString("- Interaction protocol must be followed\n\n")
	sb.WriteString("---\n\n")
}

func (pb *PromptBuilder) writeRepositoryExecutionGuardrails(sb *strings.Builder, task *queue.Task) {
	sb.WriteString("## Repository Execution Guardrails\n\n")
	if task.Type == queue.TaskTypePullRequest || task.Type == queue.TaskTypePRComment || task.Type == queue.TaskTypePRReview {
		sb.WriteString(fmt.Sprintf("- The worktree is already prepared for this task. Stay on the current local branch `%s`; do not run `git checkout`, `git switch`, or `gh pr checkout`.\n", managedWorktreeBranch(task)))
		sb.WriteString(fmt.Sprintf("- For PR review or PR-thread work, treat the current checkout as the PR head. Compare it against `%s/%s/main` by default unless the task says otherwise.\n", task.Owner, task.Repo))
		sb.WriteString(fmt.Sprintf("- If you need to push, push the current `HEAD` directly to the PR branch `%s` using `HEAD:%s`.\n", task.Branch, task.Branch))
	} else {
		sb.WriteString(fmt.Sprintf("- The worktree is already prepared for this task. Stay on the current local branch `%s`; do not run `git checkout`, `git switch`, or `gh pr checkout`.\n", managedWorktreeBranch(task)))
		sb.WriteString(fmt.Sprintf("- When you need to push, push the current `HEAD` directly to `%s` using `HEAD:%s`.\n", managedWorktreeBranch(task), managedWorktreeBranch(task)))
	}
	sb.WriteString("\n---\n\n")
}

func (pb *PromptBuilder) writeSkillCoordination(sb *strings.Builder, task *queue.Task) {
	repoSlug := fmt.Sprintf("%s/%s", task.Owner, task.Repo)
	marker := fmt.Sprintf("<!-- openagent:progress-comment %s#%d -->", repoSlug, task.Number)
	outcomeLine := "- Response / branch / follow-up link"
	if task.Type == queue.TaskTypePRReview {
		outcomeLine = "- Review outcome / review link / follow-up"
	} else if pb.getFeatureSkill(task) == issueToPRSkillName {
		outcomeLine = "- PR / branch / follow-up link"
	} else if pb.getFeatureSkill(task) == issuePlanSkillName {
		outcomeLine = "- Plan / open questions / follow-up link"
	}
	featureSkill := pb.getFeatureSkill(task)

	sb.WriteString("## Interaction Protocol\n\n")
	sb.WriteString("- The user is interacting with you on GitHub, not in a direct chat session.\n")
	sb.WriteString("- Send every user-facing progress update, final summary, and blocker notice back through GitHub.\n")
	sb.WriteString("- Write all GitHub-facing user communication in Chinese.\n")
	if featureSkill != "" {
		sb.WriteString(fmt.Sprintf("- Prefer updating the single progress comment managed by `skill %s` instead of posting separate wrap-up comments.\n", githubProgressCommentSkillName))
	} else {
		sb.WriteString(fmt.Sprintf("- For quick replies, respond directly in the thread. Only use `skill %s` when the work is substantial enough to need incremental status updates.\n", githubProgressCommentSkillName))
		sb.WriteString("- Upgrade to progress-comment mode as soon as the task requires substantial code reading, code changes, tests, more than one brief reply, or multi-step status updates.\n")
	}
	sb.WriteString("- You must post at least one user-visible GitHub comment (`gh issue comment` or `gh pr comment`) before finishing the task; never complete the task silently.\n")
	if task.Type == queue.TaskTypePRReview {
		sb.WriteString("- For PR review tasks, keep status updates in the progress comment and submit the final verdict as a formal GitHub review.\n\n")
	} else {
		sb.WriteString("- Use another GitHub surface only when the task explicitly requires it, such as a PR body or PR metadata that links the implementation back to the issue.\n\n")
	}

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
		if task.Action == "labeled" && pb.getMatchedPlanLabel(task.Labels) != "" {
			return issuePlanSkillName
		}
		if task.Action == "labeled" && pb.getMatchedLabel(task.Labels) != "" {
			return issueToPRSkillName
		}
		if task.Type == queue.TaskTypeIssueComment {
			if command, _ := matchSlashCommand(task.CommentBody, pb.commentCommands); command != "" {
				return issueToPRSkillName
			}
		}
		return ""
	}
}

func managedWorktreeBranch(task *queue.Task) string {
	if task.Type == queue.TaskTypePullRequest || task.Type == queue.TaskTypePRComment || task.Type == queue.TaskTypePRReview {
		return fmt.Sprintf("pr-%d", task.Number)
	}

	return fmt.Sprintf("issue-%d", task.Number)
}
