package service

import (
	"testing"

	"github.com/openagent/github-bridge/internal/config"
	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/session"
)

func TestGetSessionKeyTreatsPRReviewAsPullRequest(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{}
	key := svc.getSessionKey(&queue.Task{
		Type:   queue.TaskTypePRReview,
		Owner:  "openagent",
		Repo:   "github-bridge",
		Number: 99,
	})

	if key.Type != session.SessionTypePullRequest {
		t.Fatalf("expected PR session type, got %q", key.Type)
	}
}

func TestShouldProcessIssueCommentRequiresTriggerPrefixAtStartOfFirstLine(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{
		triggerConfig: config.TriggerConfig{
			Prefix: "@ogb-bot",
		},
		featuresConfig: config.FeaturesConfig{
			AIFix: config.AIFixConfig{
				Enabled:               true,
				CommentTriggerEnabled: true,
				CommentCommands:       []string{"/go"},
			},
		},
	}

	if !svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		CommentBody: "@ogb-bot hello",
	}) {
		t.Fatal("expected comment starting with trigger prefix to be processed")
	}

	if svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		CommentBody: "Some context\n@ogb-bot please handle this",
	}) {
		t.Fatal("did not expect trigger prefix on a later line to be processed")
	}

	if svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		CommentBody: "你好，直接 @ogb-bot 说明即可。",
	}) {
		t.Fatal("did not expect incidental mid-line mention to be processed")
	}
}

func TestShouldProcessIssueCommentSlashCommandTriggersCoding(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{
		featuresConfig: config.FeaturesConfig{
			AIFix: config.AIFixConfig{
				Enabled:               true,
				CommentTriggerEnabled: true,
				CommentCommands:       []string{"/go"},
			},
		},
	}

	if !svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		CommentBody: "/go implement the retry flow",
	}) {
		t.Fatal("expected /go comment to be processed")
	}

	if svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		CommentBody: "/gopher should not match /go",
	}) {
		t.Fatal("did not expect partial command prefix to be processed")
	}
}

func TestShouldProcessPlanLabelRequiresPlanSubfeatureEnabled(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{
		featuresConfig: config.FeaturesConfig{
			AIFix: config.AIFixConfig{
				Enabled:                 true,
				PlanLabelTriggerEnabled: true,
				PlanLabels:              []string{"ai-plan"},
			},
		},
	}

	if !svc.shouldProcess(&queue.Task{
		Type:   queue.TaskTypeIssue,
		Action: "labeled",
		Labels: []string{"bug", "ai-plan"},
	}) {
		t.Fatal("expected ai-plan label to be processed")
	}

	svc.featuresConfig.AIFix.PlanLabelTriggerEnabled = false
	if svc.shouldProcess(&queue.Task{
		Type:   queue.TaskTypeIssue,
		Action: "labeled",
		Labels: []string{"bug", "ai-plan"},
	}) {
		t.Fatal("did not expect ai-plan label to be processed when subfeature is disabled")
	}
}

func TestHasTriggerPrefixAtStartOfFirstLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "starts at first line",
			content: "@ogb-bot hello",
			want:    true,
		},
		{
			name:    "starts after whitespace",
			content: "   @ogb-bot hello",
			want:    true,
		},
		{
			name:    "starts on later line does not trigger",
			content: "hello\n  @ogb-bot help",
			want:    false,
		},
		{
			name:    "mid-line mention does not trigger",
			content: "Please ping @ogb-bot later",
			want:    false,
		},
		{
			name:    "empty prefix does not trigger",
			content: "@ogb-bot hello",
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			prefix := "@ogb-bot"
			if tc.name == "empty prefix does not trigger" {
				prefix = ""
			}

			if got := hasTriggerPrefixAtStartOfFirstLine(tc.content, prefix); got != tc.want {
				t.Fatalf("expected %v, got %v for content %q", tc.want, got, tc.content)
			}
		})
	}
}

func TestMatchSlashCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		content     string
		commands    []string
		wantCommand string
		wantRest    string
	}{
		{
			name:        "exact command",
			content:     "/go",
			commands:    []string{"/go"},
			wantCommand: "/go",
		},
		{
			name:        "command with trailing instruction",
			content:     "  /go implement retry\nwith tests",
			commands:    []string{"/go"},
			wantCommand: "/go",
			wantRest:    "implement retry\nwith tests",
		},
		{
			name:     "partial prefix does not match",
			content:  "/gopher implement retry",
			commands: []string{"/go"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotCommand, gotRest := matchSlashCommand(tc.content, tc.commands)
			if gotCommand != tc.wantCommand || gotRest != tc.wantRest {
				t.Fatalf("expected (%q, %q), got (%q, %q)", tc.wantCommand, tc.wantRest, gotCommand, gotRest)
			}
		})
	}
}
