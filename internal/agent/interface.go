// Package agent provides interfaces and implementations for AI agents.
//
// This package defines the Agent interface for dispatching tasks to AI coding agents.
// The primary implementation is OpenCodeAdapter which integrates with OpenCode Server.
//
// Architecture:
// - Bridge receives GitHub webhooks
// - Tasks are dispatched to Agent (fire-and-forget)
// - Agent creates isolated worktree per issue/PR
// - Agent executes task and creates PR independently
//
// References:
// - OpenCode Server: https://open-code.ai/docs/en/server
// - OpenCode SDK: https://opencode.ai/docs/sdk
package agent

import (
	"context"
)

// TaskContext contains all the context needed for dispatching a task to an AI agent.
type TaskContext struct {
	// Session key for memory management (e.g., "owner/repo/issue/123")
	SessionKey string `json:"session_key"`

	// Agent session ID for reuse (empty = create new session)
	AgentSessionID string `json:"agent_session_id,omitempty"`

	// Repository information
	RepoURL       string `json:"repo_url"`
	RepoOwner     string `json:"repo_owner"`
	RepoName      string `json:"repo_name"`
	Branch        string `json:"branch"`         // Default branch for issues, PR head ref for PR-scoped tasks
	DefaultBranch string `json:"default_branch"` // Repository default branch for issue-scoped tasks
	BaseBranch    string `json:"base_branch"`    // PR base branch for PR-scoped tasks

	// Issue/PR information
	IssueNumber int      `json:"issue_number"`
	IssueTitle  string   `json:"issue_title"`
	IssueBody   string   `json:"issue_body"`
	Labels      []string `json:"labels"`
	HeadSHA     string   `json:"head_sha,omitempty"` // PR head SHA for PR-scoped tasks

	// Event information
	EventType   string `json:"event_type"`   // "issue", "issue_comment", "pull_request", "pr_review_comment"
	EventAction string `json:"event_action"` // "opened", "created", etc.
	CommentBody string `json:"comment_body"` // The triggering comment if any
	Sender      string `json:"sender"`       // GitHub username of the requester

	// Prompt to send to the agent
	Prompt string `json:"prompt"`
}

// DispatchResult contains the result of dispatching a task.
// Since we're fire-and-forget, this only indicates if the dispatch was successful.
type DispatchResult struct {
	// Whether the task was successfully dispatched
	Dispatched bool `json:"dispatched"`

	// Task ID assigned by the agent (if any)
	TaskID string `json:"task_id,omitempty"`

	// Error message if dispatch failed
	Error string `json:"error,omitempty"`
}

// Agent defines the interface for AI agents that receive tasks.
// The agent is responsible for its own execution and GitHub feedback.
type Agent interface {
	// DispatchTask sends a task to the agent and returns immediately.
	// The agent handles execution and GitHub feedback asynchronously.
	DispatchTask(ctx context.Context, task TaskContext) (*DispatchResult, error)

	// HealthCheck verifies the agent is operational.
	HealthCheck(ctx context.Context) error

	// HealthStatus returns structured health details for the agent and its dependencies.
	HealthStatus(ctx context.Context) HealthReport
}

// AgentFactory creates agents based on configuration.
type AgentFactory func(config interface{}) (Agent, error)
