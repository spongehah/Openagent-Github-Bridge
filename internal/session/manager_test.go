package session

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/openagent/github-bridge/internal/config"
)

func TestNewManagerUsesMemoryStorage(t *testing.T) {
	t.Parallel()

	manager, err := NewManager(context.Background(), config.SessionConfig{
		Storage: "memory",
		TTL:     time.Hour,
	})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	if _, ok := manager.(*MemoryManager); !ok {
		t.Fatalf("expected MemoryManager, got %T", manager)
	}
}

func TestNewManagerUsesRedisStorage(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)

	manager, err := NewManager(context.Background(), config.SessionConfig{
		Storage: "redis",
		TTL:     time.Hour,
		Redis: config.RedisConfig{
			Addr: server.Addr(),
		},
	})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	if _, ok := manager.(*RedisManager); !ok {
		t.Fatalf("expected RedisManager, got %T", manager)
	}
}

func TestRedisManagerPersistsAndListsSessions(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	manager, err := NewRedisManager(context.Background(), config.RedisConfig{
		Addr: server.Addr(),
	}, time.Hour)
	if err != nil {
		t.Fatalf("NewRedisManager returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	key := NewSessionKey("openagent", "github-bridge", SessionTypeIssue, 42)

	sess, isNew, err := manager.GetOrCreate(key)
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if !isNew {
		t.Fatal("expected a new session on first GetOrCreate")
	}

	sess.SetAgentSessionID("session-123")
	sess.RecordDispatch("task-1", "session-123")
	if err := manager.Update(sess); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	got, err := manager.Get(key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected persisted session, got nil")
	}
	if got.AgentSessionID != "session-123" {
		t.Fatalf("expected AgentSessionID to be persisted, got %q", got.AgentSessionID)
	}
	if got.DispatchCount != 1 {
		t.Fatalf("expected DispatchCount 1, got %d", got.DispatchCount)
	}

	sessions, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session from List, got %d", len(sessions))
	}
	if sessions[0].Key != key {
		t.Fatalf("expected listed session key %q, got %q", key.String(), sessions[0].Key.String())
	}
}

func TestRedisManagerRespectsTTL(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	manager, err := NewRedisManager(context.Background(), config.RedisConfig{
		Addr: server.Addr(),
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("NewRedisManager returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	key := NewSessionKey("openagent", "github-bridge", SessionTypeIssue, 7)

	first, isNew, err := manager.GetOrCreate(key)
	if err != nil {
		t.Fatalf("first GetOrCreate returned error: %v", err)
	}
	if !isNew {
		t.Fatal("expected first GetOrCreate to create a session")
	}

	first.SetAgentSessionID("session-before-expiry")
	if err := manager.Update(first); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	server.FastForward(6 * time.Second)

	got, err := manager.Get(key)
	if err != nil {
		t.Fatalf("Get after expiration returned error: %v", err)
	}
	if got != nil {
		t.Fatal("expected expired session to be removed from Redis")
	}

	second, isNew, err := manager.GetOrCreate(key)
	if err != nil {
		t.Fatalf("second GetOrCreate returned error: %v", err)
	}
	if !isNew {
		t.Fatal("expected expired session to be recreated")
	}
	if second.AgentSessionID != "" {
		t.Fatalf("expected recreated session to have empty AgentSessionID, got %q", second.AgentSessionID)
	}
}

func TestMemoryManagerResetReplacesExistingSession(t *testing.T) {
	t.Parallel()

	manager := NewMemoryManager(time.Hour)
	key := NewSessionKey("openagent", "github-bridge", SessionTypeIssue, 99)

	first, isNew, err := manager.GetOrCreate(key)
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if !isNew {
		t.Fatal("expected first GetOrCreate to create a session")
	}

	first.SetAgentSessionID("session-before-reset")
	first.RecordDispatch("task-1", "session-before-reset")
	if err := manager.Update(first); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	reset, err := manager.Reset(key)
	if err != nil {
		t.Fatalf("Reset returned error: %v", err)
	}
	if reset.AgentSessionID != "" {
		t.Fatalf("expected reset session to clear AgentSessionID, got %q", reset.AgentSessionID)
	}
	if reset.DispatchCount != 0 {
		t.Fatalf("expected reset session to clear DispatchCount, got %d", reset.DispatchCount)
	}
	if len(reset.DispatchHistory) != 0 {
		t.Fatalf("expected reset session to clear DispatchHistory, got %d entries", len(reset.DispatchHistory))
	}

	got, err := manager.Get(key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected session after reset, got nil")
	}
	if got.AgentSessionID != "" {
		t.Fatalf("expected persisted reset session to clear AgentSessionID, got %q", got.AgentSessionID)
	}
}

func TestRedisManagerResetReplacesExistingSession(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	manager, err := NewRedisManager(context.Background(), config.RedisConfig{
		Addr: server.Addr(),
	}, time.Hour)
	if err != nil {
		t.Fatalf("NewRedisManager returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	key := NewSessionKey("openagent", "github-bridge", SessionTypeIssue, 100)

	first, isNew, err := manager.GetOrCreate(key)
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if !isNew {
		t.Fatal("expected first GetOrCreate to create a session")
	}

	first.SetAgentSessionID("session-before-reset")
	first.RecordDispatch("task-1", "session-before-reset")
	if err := manager.Update(first); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	reset, err := manager.Reset(key)
	if err != nil {
		t.Fatalf("Reset returned error: %v", err)
	}
	if reset.AgentSessionID != "" {
		t.Fatalf("expected reset session to clear AgentSessionID, got %q", reset.AgentSessionID)
	}
	if reset.DispatchCount != 0 {
		t.Fatalf("expected reset session to clear DispatchCount, got %d", reset.DispatchCount)
	}
	if len(reset.DispatchHistory) != 0 {
		t.Fatalf("expected reset session to clear DispatchHistory, got %d entries", len(reset.DispatchHistory))
	}

	got, err := manager.Get(key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected session after reset, got nil")
	}
	if got.AgentSessionID != "" {
		t.Fatalf("expected persisted reset session to clear AgentSessionID, got %q", got.AgentSessionID)
	}
}
