package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openagent/github-bridge/internal/agent"
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

	if !svc.shouldProcess(&queue.Task{
		Type:        queue.TaskTypePRComment,
		Action:      "created",
		CommentBody: "@ogb-bot review this update",
	}) {
		t.Fatal("expected PR comment starting with trigger prefix to be processed")
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

func TestGetTaskAgentName(t *testing.T) {
	t.Parallel()

	svc := &BridgeService{
		featuresConfig: config.FeaturesConfig{
			AIFix: config.AIFixConfig{
				Enabled:                 true,
				Labels:                  []string{"ai-fix"},
				PlanLabelTriggerEnabled: true,
				PlanLabels:              []string{"ai-plan"},
				CommentTriggerEnabled:   true,
				CommentCommands:         []string{"/go"},
			},
		},
		promptBuilder: NewPromptBuilder([]string{"ai-fix"}, []string{"ai-plan"}, []string{"/go"}),
	}

	cases := []struct {
		name string
		task *queue.Task
		want string
	}{
		{
			name: "plan label uses plan agent",
			task: &queue.Task{
				Type:   queue.TaskTypeIssue,
				Action: "labeled",
				Labels: []string{"bug", "ai-plan"},
			},
			want: "plan",
		},
		{
			name: "fix label uses build agent",
			task: &queue.Task{
				Type:   queue.TaskTypeIssue,
				Action: "labeled",
				Labels: []string{"ai-fix"},
			},
			want: "build",
		},
		{
			name: "slash command uses build agent",
			task: &queue.Task{
				Type:        queue.TaskTypeIssueComment,
				Action:      "created",
				CommentBody: "/go implement retry",
			},
			want: "build",
		},
		{
			name: "pr review keeps default agent",
			task: &queue.Task{
				Type:   queue.TaskTypePRReview,
				Action: "opened",
			},
			want: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := svc.getTaskAgentName(tc.task); got != tc.want {
				t.Fatalf("expected agent %q, got %q", tc.want, got)
			}
		})
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

type fakeSessionManager struct {
	sessions         map[string]*session.Session
	getOrCreateCalls int
	resetCalls       int
}

func newFakeSessionManager(existing ...*session.Session) *fakeSessionManager {
	sessions := make(map[string]*session.Session, len(existing))
	for _, sess := range existing {
		sessions[sess.Key.String()] = sess
	}
	return &fakeSessionManager{sessions: sessions}
}

func (m *fakeSessionManager) GetOrCreate(key session.SessionKey) (*session.Session, bool, error) {
	m.getOrCreateCalls++
	if sess, ok := m.sessions[key.String()]; ok {
		sess.LastActiveAt = time.Now()
		return sess, false, nil
	}

	sess := session.NewSession(key)
	m.sessions[key.String()] = sess
	return sess, true, nil
}

func (m *fakeSessionManager) Reset(key session.SessionKey) (*session.Session, error) {
	m.resetCalls++
	sess := session.NewSession(key)
	m.sessions[key.String()] = sess
	return sess, nil
}

func (m *fakeSessionManager) Get(key session.SessionKey) (*session.Session, error) {
	return m.sessions[key.String()], nil
}

func (m *fakeSessionManager) Update(sess *session.Session) error {
	m.sessions[sess.Key.String()] = sess
	return nil
}

func (m *fakeSessionManager) Delete(key session.SessionKey) error {
	delete(m.sessions, key.String())
	return nil
}

func (m *fakeSessionManager) List() ([]*session.Session, error) {
	items := make([]*session.Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		items = append(items, sess)
	}
	return items, nil
}

func (m *fakeSessionManager) StartCleanup(ctx context.Context, interval time.Duration) {}

func (m *fakeSessionManager) Close() error { return nil }

type fakeAgent struct {
	result       *agent.DispatchResult
	err          error
	dispatches   []agent.TaskContext
	lastTask     agent.TaskContext
	dispatchCall int
}

func (a *fakeAgent) DispatchTask(ctx context.Context, task agent.TaskContext) (*agent.DispatchResult, error) {
	a.dispatchCall++
	a.lastTask = task
	a.dispatches = append(a.dispatches, task)
	if a.err != nil {
		return nil, a.err
	}
	if a.result != nil {
		result := *a.result
		return &result, nil
	}
	return &agent.DispatchResult{Dispatched: true, TaskID: "agent-session-new"}, nil
}

func (a *fakeAgent) HealthCheck(ctx context.Context) error { return nil }

func (a *fakeAgent) HealthStatus(ctx context.Context) agent.HealthReport {
	return agent.HealthReport{Healthy: true}
}

func TestExtractMentionClearInstruction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantOK  bool
		want    string
	}{
		{
			name:    "clear with trailing instruction",
			content: "@ogb-bot -clear implement retry",
			wantOK:  true,
			want:    "implement retry",
		},
		{
			name:    "clear with multiline instruction",
			content: "@ogb-bot -clear\nimplement retry",
			wantOK:  true,
			want:    "implement retry",
		},
		{
			name:    "clear without instruction",
			content: "@ogb-bot    -clear   ",
			wantOK:  true,
		},
		{
			name:    "prefix collision does not match",
			content: "@ogb-bot-clear implement retry",
		},
		{
			name:    "clear later in request does not match",
			content: "@ogb-bot implement retry -clear",
		},
		{
			name:    "clear on later line does not match",
			content: "@ogb-bot\n-clear implement retry",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotOK, got := extractMentionClearInstruction(tc.content, "@ogb-bot")
			if gotOK != tc.wantOK || got != tc.want {
				t.Fatalf("expected (%v, %q), got (%v, %q)", tc.wantOK, tc.want, gotOK, got)
			}
		})
	}
}

func TestProcessMentionWithoutClearReusesExistingSession(t *testing.T) {
	t.Parallel()

	key := session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 42)
	existing := session.NewSession(key)
	existing.SetAgentSessionID("existing-agent-session")

	manager := newFakeSessionManager(existing)
	agentStub := &fakeAgent{
		result: &agent.DispatchResult{Dispatched: true, TaskID: "existing-agent-session"},
	}
	svc := NewBridgeService(
		manager,
		agentStub,
		config.TriggerConfig{Prefix: "@ogb-bot"},
		config.FeaturesConfig{},
	)

	err := svc.Process(context.Background(), &queue.Task{
		ID:          "task-1",
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		Owner:       "openagent",
		Repo:        "github-bridge",
		Number:      42,
		Title:       "Retry bug",
		Body:        "@ogb-bot continue debugging",
		IssueBody:   "Issue body",
		CommentBody: "@ogb-bot continue debugging",
		RepoURL:     "https://github.com/openagent/github-bridge.git",
		Branch:      "main",
		Sender:      "alice",
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	if manager.resetCalls != 0 {
		t.Fatalf("did not expect Reset to be called, got %d", manager.resetCalls)
	}
	if manager.getOrCreateCalls != 1 {
		t.Fatalf("expected GetOrCreate to be called once, got %d", manager.getOrCreateCalls)
	}
	if agentStub.lastTask.AgentSessionID != "existing-agent-session" {
		t.Fatalf("expected existing agent session to be reused, got %q", agentStub.lastTask.AgentSessionID)
	}
}

func TestProcessMentionClearResetsSessionAndSubsequentMentionReusesFreshSession(t *testing.T) {
	t.Parallel()

	key := session.NewSessionKey("openagent", "github-bridge", session.SessionTypeIssue, 42)
	existing := session.NewSession(key)
	existing.SetAgentSessionID("old-agent-session")
	existing.RecordDispatch("old-task", "old-agent-session")

	manager := newFakeSessionManager(existing)
	agentStub := &fakeAgent{
		result: &agent.DispatchResult{Dispatched: true, TaskID: "fresh-agent-session"},
	}
	svc := NewBridgeService(
		manager,
		agentStub,
		config.TriggerConfig{Prefix: "@ogb-bot"},
		config.FeaturesConfig{},
	)

	err := svc.Process(context.Background(), &queue.Task{
		ID:          "task-clear",
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		Owner:       "openagent",
		Repo:        "github-bridge",
		Number:      42,
		Title:       "Retry bug",
		Body:        "@ogb-bot -clear please retry\nwith tests",
		IssueBody:   "Issue body",
		CommentBody: "@ogb-bot -clear please retry\nwith tests",
		RepoURL:     "https://github.com/openagent/github-bridge.git",
		Branch:      "main",
		Sender:      "alice",
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	if manager.resetCalls != 1 {
		t.Fatalf("expected Reset to be called once, got %d", manager.resetCalls)
	}
	if manager.getOrCreateCalls != 0 {
		t.Fatalf("did not expect GetOrCreate for -clear path, got %d", manager.getOrCreateCalls)
	}
	if agentStub.lastTask.AgentSessionID != "" {
		t.Fatalf("expected -clear to force a fresh agent session, got %q", agentStub.lastTask.AgentSessionID)
	}
	if agentStub.lastTask.CommentBody != "please retry\nwith tests" {
		t.Fatalf("expected sanitized comment body, got %q", agentStub.lastTask.CommentBody)
	}
	if strings.Contains(agentStub.lastTask.Prompt, "-clear") {
		t.Fatalf("did not expect prompt to contain -clear: %q", agentStub.lastTask.Prompt)
	}
	if !strings.Contains(agentStub.lastTask.Prompt, "please retry\nwith tests") {
		t.Fatalf("expected prompt to contain sanitized request, got %q", agentStub.lastTask.Prompt)
	}

	saved := manager.sessions[key.String()]
	if saved.AgentSessionID != "fresh-agent-session" {
		t.Fatalf("expected fresh agent session to be saved, got %q", saved.AgentSessionID)
	}
	if saved.DispatchCount != 1 {
		t.Fatalf("expected fresh session dispatch count 1, got %d", saved.DispatchCount)
	}

	err = svc.Process(context.Background(), &queue.Task{
		ID:          "task-follow-up",
		Type:        queue.TaskTypeIssueComment,
		Action:      "created",
		Owner:       "openagent",
		Repo:        "github-bridge",
		Number:      42,
		Title:       "Retry bug",
		Body:        "@ogb-bot continue from the clean context",
		IssueBody:   "Issue body",
		CommentBody: "@ogb-bot continue from the clean context",
		RepoURL:     "https://github.com/openagent/github-bridge.git",
		Branch:      "main",
		Sender:      "alice",
	})
	if err != nil {
		t.Fatalf("follow-up Process returned error: %v", err)
	}

	if manager.getOrCreateCalls != 1 {
		t.Fatalf("expected follow-up call to reuse GetOrCreate once, got %d", manager.getOrCreateCalls)
	}
	if agentStub.lastTask.AgentSessionID != "fresh-agent-session" {
		t.Fatalf("expected follow-up request to reuse fresh agent session, got %q", agentStub.lastTask.AgentSessionID)
	}
}
