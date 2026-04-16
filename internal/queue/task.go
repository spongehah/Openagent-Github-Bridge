// Package queue provides an asynchronous task queue for processing webhook events.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// TaskType represents the type of task.
type TaskType string

const (
	TaskTypeIssue        TaskType = "issue"
	TaskTypeIssueComment TaskType = "issue_comment"
	TaskTypePullRequest  TaskType = "pull_request"
	TaskTypePRComment    TaskType = "pr_comment"
	TaskTypePRReview     TaskType = "pr_review" // Auto PR review task
)

// Task represents a unit of work to be processed.
type Task struct {
	ID        string    `json:"id"`
	Type      TaskType  `json:"type"`
	EventType string    `json:"event_type"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`

	// Repository info
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	RepoURL string `json:"repo_url"`
	Branch  string `json:"branch"`

	// Issue/PR info
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`

	// For issue comments
	IssueBody   string `json:"issue_body,omitempty"`
	CommentID   int64  `json:"comment_id,omitempty"`
	CommentBody string `json:"comment_body,omitempty"`

	// For PR
	HeadSHA string `json:"head_sha,omitempty"`
	IsDraft bool   `json:"is_draft,omitempty"` // Whether the PR is a draft
	BaseBranch string `json:"base_branch,omitempty"` // Target branch for PR

	// For PR review comments
	FilePath string `json:"file_path,omitempty"`

	// Sender info
	Sender     string `json:"sender"`
	SenderType string `json:"sender_type,omitempty"` // "User" or "Bot"

	// Processing metadata
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// TaskProcessor defines the interface for processing tasks.
type TaskProcessor interface {
	Process(ctx context.Context, task *Task) error
}

// TaskQueue manages the async task queue using channels.
type TaskQueue struct {
	tasks     chan *Task
	closed    bool
	closeChan chan struct{}
}

// NewTaskQueue creates a new task queue with the given buffer size.
func NewTaskQueue(bufferSize int) *TaskQueue {
	return &TaskQueue{
		tasks:     make(chan *Task, bufferSize),
		closeChan: make(chan struct{}),
	}
}

// Enqueue adds a task to the queue.
// Returns an error if the queue is full or closed.
func (q *TaskQueue) Enqueue(task *Task) error {
	if task == nil {
		return nil
	}

	if q.closed {
		return fmt.Errorf("queue is closed")
	}

	task.CreatedAt = time.Now()

	select {
	case q.tasks <- task:
		log.Printf("Task %s enqueued: %s/%s#%d", task.ID, task.Owner, task.Repo, task.Number)
		return nil
	default:
		return fmt.Errorf("queue is full")
	}
}

// StartWorker starts a worker goroutine that processes tasks.
func (q *TaskQueue) StartWorker(ctx context.Context, processor TaskProcessor) {
	for {
		select {
		case <-ctx.Done():
			log.Println("Worker shutting down...")
			return
		case <-q.closeChan:
			log.Println("Worker received close signal...")
			return
		case task, ok := <-q.tasks:
			if !ok {
				log.Println("Task channel closed, worker exiting...")
				return
			}
			q.processTask(ctx, task, processor)
		}
	}
}

// processTask handles a single task with retry logic.
func (q *TaskQueue) processTask(ctx context.Context, task *Task, processor TaskProcessor) {
	const maxAttempts = 3

	task.StartedAt = time.Now()
	task.Attempts++

	log.Printf("Processing task %s (attempt %d/%d): %s %s/%s#%d",
		task.ID, task.Attempts, maxAttempts, task.Type, task.Owner, task.Repo, task.Number)

	err := processor.Process(ctx, task)
	if err != nil {
		task.LastError = err.Error()
		log.Printf("Task %s failed: %v", task.ID, err)

		// Retry if under max attempts
		if task.Attempts < maxAttempts {
			// Exponential backoff
			backoff := time.Duration(task.Attempts*task.Attempts) * time.Second
			time.Sleep(backoff)

			// Re-enqueue for retry
			select {
			case q.tasks <- task:
				log.Printf("Task %s re-enqueued for retry", task.ID)
			default:
				log.Printf("Failed to re-enqueue task %s: queue full", task.ID)
			}
		} else {
			log.Printf("Task %s exceeded max attempts, giving up", task.ID)
		}
		return
	}

	task.FinishedAt = time.Now()
	log.Printf("Task %s completed successfully in %v", task.ID, task.FinishedAt.Sub(task.StartedAt))
}

// Close shuts down the task queue gracefully.
func (q *TaskQueue) Close() {
	q.closed = true
	close(q.closeChan)
	close(q.tasks)
}

// Len returns the current number of tasks in the queue.
func (q *TaskQueue) Len() int {
	return len(q.tasks)
}

// GenerateID creates a unique task identifier.
func GenerateID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(bytes))
}

// SessionKeyFromTask generates a session key from a task.
func SessionKeyFromTask(task *Task) string {
	taskType := "issue"
	if task.Type == TaskTypePullRequest || task.Type == TaskTypePRComment {
		taskType = "pr"
	}
	return fmt.Sprintf("%s/%s/%s/%d", task.Owner, task.Repo, taskType, task.Number)
}
