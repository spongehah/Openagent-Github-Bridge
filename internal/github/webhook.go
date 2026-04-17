// Package github provides GitHub API client and webhook utilities.
//
// References:
// - GitHub Webhooks: https://docs.github.com/en/webhooks
// - Webhook Events: https://docs.github.com/en/webhooks/webhook-events-and-payloads
// - Securing Webhooks: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Common webhook errors
var (
	ErrInvalidSignature = errors.New("invalid webhook signature")
	ErrMissingSignature = errors.New("missing X-Hub-Signature-256 header")
	ErrUnsupportedEvent = errors.New("unsupported webhook event")
)

// WebhookEvent represents a parsed GitHub webhook event.
type WebhookEvent struct {
	Type    string      // Event type (e.g., "issues", "issue_comment", "pull_request")
	Action  string      // Event action (e.g., "opened", "created", "synchronize")
	Payload interface{} // Parsed event payload
}

// IssueEvent represents an issue event payload.
// Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
type IssueEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// IssueCommentEvent represents an issue comment event payload.
// Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#issue_comment
type IssueCommentEvent struct {
	Action  string `json:"action"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Issue struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		State       string `json:"state"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request,omitempty"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// PullRequestEvent represents a pull request event payload.
// Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#pull_request
type PullRequestEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
		Draft  bool   `json:"draft"` // Whether the PR is a draft
		Head   struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"` // "User" or "Bot"
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// PullRequestReviewCommentEvent represents a PR review comment event payload.
// Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#pull_request_review_comment
type PullRequestReviewCommentEvent struct {
	Action  string `json:"action"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		Path string `json:"path"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// VerifySignature validates the webhook payload signature using HMAC-SHA256.
// The signature header format is "sha256=<hex-encoded-signature>".
//
// Reference: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
func VerifySignature(payload []byte, signature string, secret string) error {
	if signature == "" {
		return ErrMissingSignature
	}

	// Extract the hash from "sha256=..." format
	if !strings.HasPrefix(signature, "sha256=") {
		return ErrInvalidSignature
	}

	receivedSig, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return ErrInvalidSignature
	}

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedSig := mac.Sum(nil)

	// Constant-time comparison to prevent timing attacks
	if !hmac.Equal(receivedSig, expectedSig) {
		return ErrInvalidSignature
	}

	return nil
}

// ParseWebhookEvent parses a raw webhook payload into a typed event.
func ParseWebhookEvent(eventType string, payload []byte) (*WebhookEvent, error) {
	event := &WebhookEvent{
		Type: eventType,
	}

	switch eventType {
	case "issues":
		var issueEvent IssueEvent
		if err := json.Unmarshal(payload, &issueEvent); err != nil {
			return nil, fmt.Errorf("failed to parse issue event: %w", err)
		}
		event.Action = issueEvent.Action
		event.Payload = &issueEvent

	case "issue_comment":
		var commentEvent IssueCommentEvent
		if err := json.Unmarshal(payload, &commentEvent); err != nil {
			return nil, fmt.Errorf("failed to parse issue comment event: %w", err)
		}
		event.Action = commentEvent.Action
		event.Payload = &commentEvent

	case "pull_request":
		var prEvent PullRequestEvent
		if err := json.Unmarshal(payload, &prEvent); err != nil {
			return nil, fmt.Errorf("failed to parse pull request event: %w", err)
		}
		event.Action = prEvent.Action
		event.Payload = &prEvent

	case "pull_request_review_comment":
		var reviewCommentEvent PullRequestReviewCommentEvent
		if err := json.Unmarshal(payload, &reviewCommentEvent); err != nil {
			return nil, fmt.Errorf("failed to parse PR review comment event: %w", err)
		}
		event.Action = reviewCommentEvent.Action
		event.Payload = &reviewCommentEvent

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedEvent, eventType)
	}

	return event, nil
}

// SupportedEvents returns a list of supported webhook event types.
func SupportedEvents() []string {
	return []string{
		"issues",
		"issue_comment",
		"pull_request",
		"pull_request_review_comment",
	}
}
