// Package session provides session management for maintaining context
// across multiple interactions with the same GitHub issue or PR.
package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SessionType represents the type of GitHub entity.
type SessionType string

const (
	SessionTypeIssue       SessionType = "issue"
	SessionTypePullRequest SessionType = "pr"
)

// SessionKey uniquely identifies a session based on repo and issue/PR number.
// Format: {owner}/{repo}/{type}/{number}
type SessionKey struct {
	Owner  string
	Repo   string
	Type   SessionType
	Number int
}

// String returns the string representation of the session key.
func (k SessionKey) String() string {
	return fmt.Sprintf("%s/%s/%s/%d", k.Owner, k.Repo, k.Type, k.Number)
}

// NewSessionKey creates a new session key from components.
func NewSessionKey(owner, repo string, sessionType SessionType, number int) SessionKey {
	return SessionKey{
		Owner:  owner,
		Repo:   repo,
		Type:   sessionType,
		Number: number,
	}
}

// DispatchRecord represents a single task dispatch to the agent.
type DispatchRecord struct {
	BridgeTaskID string    `json:"bridge_task_id"` // Task ID from Bridge
	AgentTaskID  string    `json:"agent_task_id"`  // Task ID from Agent (e.g., OpenCode)
	DispatchedAt time.Time `json:"dispatched_at"`  // When the task was dispatched
}

// Session represents a conversation session tied to a GitHub issue or PR.
// The session tracks dispatches to the agent for monitoring purposes.
type Session struct {
	Key             SessionKey       `json:"key"`
	AgentSessionID  string           `json:"agent_session_id,omitempty"` // Agent's session ID (e.g., OpenCode session ID) for reuse
	DispatchHistory []DispatchRecord `json:"dispatch_history"`           // History of dispatched tasks
	CreatedAt       time.Time        `json:"created_at"`                 // When the session was created
	LastActiveAt    time.Time        `json:"last_active_at"`             // Last activity timestamp
	DispatchCount   int              `json:"dispatch_count"`             // Total dispatches in session
}

// NewSession creates a new session with the given key.
func NewSession(key SessionKey) *Session {
	now := time.Now()
	return &Session{
		Key:             key,
		DispatchHistory: make([]DispatchRecord, 0),
		CreatedAt:       now,
		LastActiveAt:    now,
		DispatchCount:   0,
	}
}

// SetAgentSessionID sets the agent's session ID for reuse.
func (s *Session) SetAgentSessionID(id string) {
	s.AgentSessionID = id
	s.LastActiveAt = time.Now()
}

// HasAgentSession returns true if the session has an agent session ID.
func (s *Session) HasAgentSession() bool {
	return s.AgentSessionID != ""
}

// RecordDispatch records a task dispatch to the agent.
func (s *Session) RecordDispatch(bridgeTaskID, agentTaskID string) {
	s.DispatchHistory = append(s.DispatchHistory, DispatchRecord{
		BridgeTaskID: bridgeTaskID,
		AgentTaskID:  agentTaskID,
		DispatchedAt: time.Now(),
	})
	s.DispatchCount++
	s.LastActiveAt = time.Now()
}

// GetRecentDispatches returns the most recent N dispatch records.
func (s *Session) GetRecentDispatches(n int) []DispatchRecord {
	if n >= len(s.DispatchHistory) {
		return s.DispatchHistory
	}
	return s.DispatchHistory[len(s.DispatchHistory)-n:]
}

// IsExpired checks if the session has exceeded the TTL.
func (s *Session) IsExpired(ttl time.Duration) bool {
	return time.Since(s.LastActiveAt) > ttl
}

// Manager defines the interface for session management.
type Manager interface {
	// GetOrCreate retrieves an existing session or creates a new one.
	GetOrCreate(key SessionKey) (*Session, bool, error)

	// Reset replaces any existing session with a fresh one for the same key.
	Reset(key SessionKey) (*Session, error)

	// Get retrieves a session by key, returns nil if not found.
	Get(key SessionKey) (*Session, error)

	// Update saves changes to an existing session.
	Update(session *Session) error

	// Delete removes a session.
	Delete(key SessionKey) error

	// List returns all active sessions.
	List() ([]*Session, error)

	// StartCleanup starts a background goroutine to clean expired sessions.
	StartCleanup(ctx context.Context, interval time.Duration)

	// Close releases any resources held by the manager.
	Close() error
}

// MemoryManager implements Manager using in-memory storage.
type MemoryManager struct {
	sessions sync.Map // map[string]*Session
	ttl      time.Duration
}

// NewMemoryManager creates a new in-memory session manager.
func NewMemoryManager(ttl time.Duration) *MemoryManager {
	return &MemoryManager{
		ttl: ttl,
	}
}

// GetOrCreate retrieves an existing session or creates a new one.
// Returns (session, isNew, error).
func (m *MemoryManager) GetOrCreate(key SessionKey) (*Session, bool, error) {
	keyStr := key.String()

	// Try to load existing session
	if val, ok := m.sessions.Load(keyStr); ok {
		session := val.(*Session)
		// Check if expired
		if session.IsExpired(m.ttl) {
			// Delete expired session and create new one
			m.sessions.Delete(keyStr)
		} else {
			session.LastActiveAt = time.Now()
			return session, false, nil
		}
	}

	// Create new session
	session := NewSession(key)
	m.sessions.Store(keyStr, session)
	return session, true, nil
}

// Reset replaces any existing session for the key with a fresh session.
func (m *MemoryManager) Reset(key SessionKey) (*Session, error) {
	session := NewSession(key)
	m.sessions.Store(key.String(), session)
	return session, nil
}

// Get retrieves a session by key.
func (m *MemoryManager) Get(key SessionKey) (*Session, error) {
	keyStr := key.String()
	if val, ok := m.sessions.Load(keyStr); ok {
		session := val.(*Session)
		if session.IsExpired(m.ttl) {
			m.sessions.Delete(keyStr)
			return nil, nil
		}
		return session, nil
	}
	return nil, nil
}

// Update saves changes to a session.
func (m *MemoryManager) Update(session *Session) error {
	session.LastActiveAt = time.Now()
	m.sessions.Store(session.Key.String(), session)
	return nil
}

// Delete removes a session.
func (m *MemoryManager) Delete(key SessionKey) error {
	m.sessions.Delete(key.String())
	return nil
}

// List returns all active sessions.
func (m *MemoryManager) List() ([]*Session, error) {
	var sessions []*Session
	m.sessions.Range(func(key, value interface{}) bool {
		session := value.(*Session)
		if !session.IsExpired(m.ttl) {
			sessions = append(sessions, session)
		}
		return true
	})
	return sessions, nil
}

// StartCleanup starts a background goroutine to periodically clean expired sessions.
func (m *MemoryManager) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpired()
		}
	}
}

// Close releases resources held by the memory-backed session manager.
func (m *MemoryManager) Close() error {
	return nil
}

// cleanupExpired removes all expired sessions.
func (m *MemoryManager) cleanupExpired() {
	var keysToDelete []string
	m.sessions.Range(func(key, value interface{}) bool {
		session := value.(*Session)
		if session.IsExpired(m.ttl) {
			keysToDelete = append(keysToDelete, key.(string))
		}
		return true
	})

	for _, key := range keysToDelete {
		m.sessions.Delete(key)
	}
}

// Count returns the number of active sessions.
func (m *MemoryManager) Count() int {
	count := 0
	m.sessions.Range(func(key, value interface{}) bool {
		session := value.(*Session)
		if !session.IsExpired(m.ttl) {
			count++
		}
		return true
	})
	return count
}

// GetSessionKeyForIssue creates a session key for an issue.
func GetSessionKeyForIssue(owner, repo string, number int) SessionKey {
	return NewSessionKey(owner, repo, SessionTypeIssue, number)
}

// GetSessionKeyForPR creates a session key for a pull request.
func GetSessionKeyForPR(owner, repo string, number int) SessionKey {
	return NewSessionKey(owner, repo, SessionTypePullRequest, number)
}
