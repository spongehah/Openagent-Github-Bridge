// Package service provides the core business logic for the bridge.
package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/openagent/github-bridge/internal/agent"
	"github.com/openagent/github-bridge/internal/config"
	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

// BridgeService orchestrates the dispatching of GitHub events to AI agents.
// It follows a fire-and-forget pattern: dispatch task and return immediately.
// The agent (e.g., OpenCode) handles execution and GitHub feedback.
type BridgeService struct {
	sessionManager session.Manager
	agent          agent.Agent
	triggerConfig  config.TriggerConfig
	featuresConfig config.FeaturesConfig
	promptBuilder  *PromptBuilder
}

// NewBridgeService creates a new bridge service.
func NewBridgeService(
	sm session.Manager,
	ag agent.Agent,
	tc config.TriggerConfig,
	fc config.FeaturesConfig,
) *BridgeService {
	return &BridgeService{
		sessionManager: sm,
		agent:          ag,
		triggerConfig:  tc,
		featuresConfig: fc,
		promptBuilder:  NewPromptBuilder(fc.AIFix.Labels),
	}
}

// Process handles a task from the queue.
// This implements the queue.TaskProcessor interface.
func (s *BridgeService) Process(ctx context.Context, task *queue.Task) error {
	log.Printf("[Bridge] Processing task: %s %s/%s#%d from @%s",
		task.Type, task.Owner, task.Repo, task.Number, task.Sender)

	// Check if we should process this task
	if !s.shouldProcess(task) {
		log.Printf("[Bridge] Skipping task %s: does not match trigger criteria", task.ID)
		return nil
	}

	// Get or create session for this issue/PR
	sessionKey := s.getSessionKey(task)
	sess, isNew, err := s.sessionManager.GetOrCreate(sessionKey)
	if err != nil {
		return fmt.Errorf("failed to get/create session: %w", err)
	}

	log.Printf("[Bridge] Session %s (new: %v) for task %s", sessionKey.String(), isNew, task.ID)

	// Build prompt for the agent
	prompt := s.promptBuilder.Build(task, sess, isNew)

	// Build task context
	// If session already has an agent session ID, pass it for reuse
	taskContext := agent.TaskContext{
		SessionKey:     sessionKey.String(),
		AgentSessionID: sess.AgentSessionID, // Reuse existing agent session if available
		RepoURL:        task.RepoURL,
		RepoOwner:      task.Owner,
		RepoName:       task.Repo,
		Branch:         task.Branch,
		DefaultBranch:  s.getDefaultBranch(task),
		BaseBranch:     task.BaseBranch,
		IssueNumber:    task.Number,
		IssueTitle:     task.Title,
		IssueBody:      s.getIssueBody(task),
		Labels:         task.Labels,
		HeadSHA:        task.HeadSHA,
		EventType:      string(task.Type),
		EventAction:    task.Action,
		CommentBody:    task.CommentBody,
		Sender:         task.Sender,
		Prompt:         prompt,
	}

	// Dispatch to agent (fire-and-forget)
	result, err := s.agent.DispatchTask(ctx, taskContext)
	if err != nil {
		log.Printf("[Bridge] Failed to dispatch task %s: %v", task.ID, err)
		return fmt.Errorf("failed to dispatch task: %w", err)
	}

	if !result.Dispatched {
		log.Printf("[Bridge] Task %s was not accepted: %s", task.ID, result.Error)
		return fmt.Errorf("task not accepted: %s", result.Error)
	}

	// Save agent session ID for future reuse (if this is a new session)
	if !sess.HasAgentSession() && result.TaskID != "" {
		sess.SetAgentSessionID(result.TaskID)
	}

	// Record dispatch in session (for tracking purposes)
	sess.RecordDispatch(task.ID, result.TaskID)
	if err := s.sessionManager.Update(sess); err != nil {
		log.Printf("[Bridge] Warning: failed to update session: %v", err)
	}

	log.Printf("[Bridge] Task %s dispatched successfully (agent task ID: %s)", task.ID, result.TaskID)
	return nil
}

// shouldProcess determines if a task should be processed based on trigger config.
func (s *BridgeService) shouldProcess(task *queue.Task) bool {
	// Handle PR review tasks
	if task.Type == queue.TaskTypePRReview {
		return s.shouldProcessPRReview(task)
	}

	// Always process if configured to respond to all issues
	if s.triggerConfig.RespondAllIssues {
		return true
	}

	// Check if the task is triggered by a configured label (ai-fix)
	if s.isLabelTriggered(task) {
		return true
	}

	// Check if the comment/body contains the trigger prefix
	content := task.Body
	if task.CommentBody != "" {
		content = task.CommentBody
	}

	return strings.Contains(strings.ToLower(content), strings.ToLower(s.triggerConfig.Prefix))
}

// shouldProcessPRReview determines if a PR should be auto-reviewed.
func (s *BridgeService) shouldProcessPRReview(task *queue.Task) bool {
	// Check if this is a label-triggered PR review
	if task.Action == "labeled" {
		return s.isPRReviewLabelTriggered(task)
	}

	// For "opened" action, check if auto PR review on opened is enabled
	if !s.featuresConfig.PRReview.Enabled {
		log.Printf("[Bridge] PR review on opened feature is disabled, skipping task %s", task.ID)
		return false
	}

	// Skip draft PRs if configured (and if we can detect it)
	if s.featuresConfig.PRReview.SkipDraftPRs && task.IsDraft {
		log.Printf("[Bridge] Skipping draft PR #%d", task.Number)
		return false
	}

	// Skip bot PRs if configured
	if s.featuresConfig.PRReview.SkipBotPRs && task.SenderType == "Bot" {
		log.Printf("[Bridge] Skipping bot PR #%d by %s", task.Number, task.Sender)
		return false
	}

	return true
}

// isPRReviewLabelTriggered checks if the PR has a review trigger label (e.g., "ai-review").
// This does NOT check draft/bot status - label trigger reviews all PRs with the label.
func (s *BridgeService) isPRReviewLabelTriggered(task *queue.Task) bool {
	// Check if label trigger feature is enabled
	if !s.featuresConfig.PRReview.LabelTriggerEnabled {
		log.Printf("[Bridge] PR review label trigger is disabled, skipping task %s", task.ID)
		return false
	}

	// Check if the PR has any of the configured review trigger labels
	for _, taskLabel := range task.Labels {
		for _, triggerLabel := range s.featuresConfig.PRReview.Labels {
			if strings.EqualFold(taskLabel, triggerLabel) {
				log.Printf("[Bridge] PR #%d triggered by label: %s", task.Number, taskLabel)
				return true
			}
		}
	}

	log.Printf("[Bridge] PR #%d does not have a review trigger label", task.Number)
	return false
}

// isLabelTriggered checks if the task has a trigger label (e.g., "ai-fix").
func (s *BridgeService) isLabelTriggered(task *queue.Task) bool {
	// Check if ai-fix feature is enabled
	if !s.featuresConfig.AIFix.Enabled {
		return false
	}

	// Only check labeled events
	if task.Action != "labeled" {
		return false
	}

	for _, taskLabel := range task.Labels {
		for _, triggerLabel := range s.featuresConfig.AIFix.Labels {
			if strings.EqualFold(taskLabel, triggerLabel) {
				log.Printf("[Bridge] Issue #%d triggered by ai-fix label: %s", task.Number, taskLabel)
				return true
			}
		}
	}
	return false
}

// getMatchedTriggerLabel returns the first matched trigger label if any.
func (s *BridgeService) getMatchedTriggerLabel(task *queue.Task) string {
	for _, taskLabel := range task.Labels {
		for _, triggerLabel := range s.triggerConfig.Labels {
			if strings.EqualFold(taskLabel, triggerLabel) {
				return triggerLabel
			}
		}
	}
	return ""
}

// getSessionKey creates a session key from the task.
func (s *BridgeService) getSessionKey(task *queue.Task) session.SessionKey {
	sessionType := session.SessionTypeIssue
	if task.Type == queue.TaskTypePullRequest || task.Type == queue.TaskTypePRComment || task.Type == queue.TaskTypePRReview {
		sessionType = session.SessionTypePullRequest
	}
	return session.NewSessionKey(task.Owner, task.Repo, sessionType, task.Number)
}

// getDefaultBranch returns the repository default branch for issue-scoped tasks.
func (s *BridgeService) getDefaultBranch(task *queue.Task) string {
	switch task.Type {
	case queue.TaskTypeIssue, queue.TaskTypeIssueComment:
		return task.Branch
	default:
		return ""
	}
}

// getIssueBody keeps the issue content populated even for non-comment issue events.
func (s *BridgeService) getIssueBody(task *queue.Task) string {
	if task.IssueBody != "" {
		return task.IssueBody
	}
	return task.Body
}

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
