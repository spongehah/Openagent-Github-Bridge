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

	return hasTriggerPrefixAtStartOfFirstLine(content, s.triggerConfig.Prefix)
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

func hasTriggerPrefixAtStartOfFirstLine(content, prefix string) bool {
	if prefix == "" {
		return false
	}

	normalizedPrefix := strings.ToLower(strings.TrimSpace(prefix))
	firstLine := content
	if idx := strings.Index(firstLine, "\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}

	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(firstLine)), normalizedPrefix)
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
