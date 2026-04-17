// Package handler provides HTTP handlers for the application.
package handler

import (
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openagent/github-bridge/internal/config"
	ghwebhook "github.com/openagent/github-bridge/internal/github"
	"github.com/openagent/github-bridge/internal/queue"
)

// WebhookHandler handles incoming GitHub webhook events.
type WebhookHandler struct {
	githubConfig config.GitHubConfig
	taskQueue    *queue.TaskQueue
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(cfg config.GitHubConfig, tq *queue.TaskQueue) *WebhookHandler {
	return &WebhookHandler{
		githubConfig: cfg,
		taskQueue:    tq,
	}
}

// HandleWebhook processes incoming GitHub webhook requests.
// It verifies the signature, parses the event, and enqueues it for async processing.
func (h *WebhookHandler) HandleWebhook(c *gin.Context) {
	// Read request body
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Get signature header
	signature := c.GetHeader("X-Hub-Signature-256")

	// Verify webhook signature
	if err := ghwebhook.VerifySignature(payload, signature, h.githubConfig.WebhookSecret); err != nil {
		log.Printf("Webhook signature verification failed: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Get event type
	eventType := c.GetHeader("X-GitHub-Event")
	if eventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing X-GitHub-Event header"})
		return
	}

	// Skip ping events
	if eventType == "ping" {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
		return
	}

	// Parse the webhook event
	event, err := ghwebhook.ParseWebhookEvent(eventType, payload)
	if err != nil {
		if err == ghwebhook.ErrUnsupportedEvent {
			// Return 200 for unsupported events to acknowledge receipt
			c.JSON(http.StatusOK, gin.H{"message": "event type not handled"})
			return
		}
		log.Printf("Failed to parse webhook event: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event"})
		return
	}

	// Create task from event
	task, err := h.createTaskFromEvent(event)
	if err != nil {
		log.Printf("Failed to create task from event: %v", err)
		c.JSON(http.StatusOK, gin.H{"message": "event not actionable"})
		return
	}
	if task == nil {
		c.JSON(http.StatusOK, gin.H{"message": "event not actionable"})
		return
	}

	// Enqueue the task for async processing
	if err := h.taskQueue.Enqueue(task); err != nil {
		log.Printf("Failed to enqueue task: %v", err)
		// Still return 202 to not block GitHub
		c.JSON(http.StatusAccepted, gin.H{"message": "queued with warning", "warning": "queue may be full"})
		return
	}

	// Return 202 Accepted immediately (async processing)
	c.JSON(http.StatusAccepted, gin.H{
		"message": "event received and queued",
		"task_id": task.ID,
	})
}

// createTaskFromEvent converts a webhook event into a processable task.
func (h *WebhookHandler) createTaskFromEvent(event *ghwebhook.WebhookEvent) (*queue.Task, error) {
	switch payload := event.Payload.(type) {
	case *ghwebhook.IssueEvent:
		// Handle opened and labeled actions
		if event.Action != "opened" && event.Action != "labeled" {
			return nil, nil
		}
		// Extract labels
		labels := make([]string, 0, len(payload.Issue.Labels))
		for _, l := range payload.Issue.Labels {
			labels = append(labels, l.Name)
		}
		return &queue.Task{
			ID:        generateTaskID(),
			Type:      queue.TaskTypeIssue,
			EventType: event.Type,
			Action:    event.Action,
			Owner:     payload.Repository.Owner.Login,
			Repo:      payload.Repository.Name,
			Number:    payload.Issue.Number,
			Title:     payload.Issue.Title,
			Body:      payload.Issue.Body,
			Labels:    labels,
			IssueBody: payload.Issue.Body,
			Sender:    payload.Sender.Login,
			RepoURL:   payload.Repository.CloneURL,
			Branch:    payload.Repository.DefaultBranch,
		}, nil

	case *ghwebhook.IssueCommentEvent:
		// Only handle created comments
		if event.Action != "created" {
			return nil, nil
		}
		// GitHub issue_comment covers both issues and PR conversations.
		// Ignore PR discussion comments so slash commands like /go stay issue-scoped.
		if payload.Issue.PullRequest != nil {
			return nil, nil
		}
		return &queue.Task{
			ID:          generateTaskID(),
			Type:        queue.TaskTypeIssueComment,
			EventType:   event.Type,
			Action:      event.Action,
			Owner:       payload.Repository.Owner.Login,
			Repo:        payload.Repository.Name,
			Number:      payload.Issue.Number,
			Title:       payload.Issue.Title,
			Body:        payload.Comment.Body,
			IssueBody:   payload.Issue.Body,
			Sender:      payload.Sender.Login,
			RepoURL:     payload.Repository.CloneURL,
			Branch:      payload.Repository.DefaultBranch,
			CommentID:   payload.Comment.ID,
			CommentBody: payload.Comment.Body,
		}, nil

	case *ghwebhook.PullRequestEvent:
		// Handle opened (for PR review), labeled (for label-triggered review), and synchronize events
		if event.Action != "opened" && event.Action != "synchronize" && event.Action != "labeled" {
			return nil, nil
		}
		// Determine task type based on action
		taskType := queue.TaskTypePullRequest
		if event.Action == "opened" || event.Action == "labeled" {
			taskType = queue.TaskTypePRReview
		}
		// Extract labels
		labels := make([]string, 0, len(payload.PullRequest.Labels))
		for _, l := range payload.PullRequest.Labels {
			labels = append(labels, l.Name)
		}
		return &queue.Task{
			ID:         generateTaskID(),
			Type:       taskType,
			EventType:  event.Type,
			Action:     event.Action,
			Owner:      payload.Repository.Owner.Login,
			Repo:       payload.Repository.Name,
			Number:     payload.PullRequest.Number,
			Title:      payload.PullRequest.Title,
			Body:       payload.PullRequest.Body,
			Labels:     labels,
			Sender:     payload.Sender.Login,
			SenderType: payload.PullRequest.User.Type,
			RepoURL:    payload.Repository.CloneURL,
			Branch:     payload.PullRequest.Head.Ref,
			BaseBranch: payload.PullRequest.Base.Ref,
			HeadSHA:    payload.PullRequest.Head.SHA,
			IsDraft:    payload.PullRequest.Draft,
		}, nil

	case *ghwebhook.PullRequestReviewCommentEvent:
		if event.Action != "created" {
			return nil, nil
		}
		return &queue.Task{
			ID:          generateTaskID(),
			Type:        queue.TaskTypePRComment,
			EventType:   event.Type,
			Action:      event.Action,
			Owner:       payload.Repository.Owner.Login,
			Repo:        payload.Repository.Name,
			Number:      payload.PullRequest.Number,
			Title:       payload.PullRequest.Title,
			Body:        payload.Comment.Body,
			Sender:      payload.Sender.Login,
			RepoURL:     payload.Repository.CloneURL,
			CommentID:   payload.Comment.ID,
			CommentBody: payload.Comment.Body,
			FilePath:    payload.Comment.Path,
		}, nil

	default:
		return nil, nil
	}
}

// generateTaskID creates a unique task identifier.
func generateTaskID() string {
	return queue.GenerateID()
}
