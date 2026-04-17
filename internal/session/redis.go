package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openagent/github-bridge/internal/config"
	"github.com/redis/go-redis/v9"
)

const redisSessionKeyPrefix = "openagent-github-bridge:session:"

// RedisManager implements Manager using Redis storage.
type RedisManager struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisManager creates a Redis-backed session manager.
// Reference: https://github.com/redis/go-redis
func NewRedisManager(ctx context.Context, cfg config.RedisConfig, ttl time.Duration) (*RedisManager, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisManager{
		client: client,
		ttl:    ttl,
	}, nil
}

// GetOrCreate retrieves an existing session or creates a new one.
func (m *RedisManager) GetOrCreate(key SessionKey) (*Session, bool, error) {
	sess, err := m.Get(key)
	if err != nil {
		return nil, false, err
	}
	if sess != nil {
		sess.LastActiveAt = time.Now()
		if err := m.persist(sess); err != nil {
			return nil, false, err
		}
		return sess, false, nil
	}

	sess = NewSession(key)
	payload, err := m.marshalSession(sess)
	if err != nil {
		return nil, false, err
	}

	created, err := m.client.SetNX(context.Background(), m.redisKey(key), payload, m.ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("create session in redis: %w", err)
	}
	if created {
		return sess, true, nil
	}

	sess, err = m.Get(key)
	if err != nil {
		return nil, false, err
	}
	if sess == nil {
		return nil, false, fmt.Errorf("session disappeared during concurrent creation: %s", key.String())
	}

	sess.LastActiveAt = time.Now()
	if err := m.persist(sess); err != nil {
		return nil, false, err
	}
	return sess, false, nil
}

// Reset replaces any existing session for the key with a fresh session.
func (m *RedisManager) Reset(key SessionKey) (*Session, error) {
	sess := NewSession(key)
	if err := m.persist(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Get retrieves a session by key.
func (m *RedisManager) Get(key SessionKey) (*Session, error) {
	payload, err := m.client.Get(context.Background(), m.redisKey(key)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session from redis: %w", err)
	}

	sess, err := m.unmarshalSession(payload)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// Update saves changes to a session.
func (m *RedisManager) Update(session *Session) error {
	session.LastActiveAt = time.Now()
	return m.persist(session)
}

// Delete removes a session.
func (m *RedisManager) Delete(key SessionKey) error {
	if err := m.client.Del(context.Background(), m.redisKey(key)).Err(); err != nil {
		return fmt.Errorf("delete session from redis: %w", err)
	}
	return nil
}

// List returns all active sessions.
func (m *RedisManager) List() ([]*Session, error) {
	var (
		cursor   uint64
		sessions []*Session
		ctx      = context.Background()
	)

	for {
		keys, nextCursor, err := m.client.Scan(ctx, cursor, redisSessionKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan sessions from redis: %w", err)
		}

		for _, key := range keys {
			payload, err := m.client.Get(ctx, key).Result()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("get listed session from redis: %w", err)
			}

			sess, err := m.unmarshalSession(payload)
			if err != nil {
				return nil, err
			}
			sessions = append(sessions, sess)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return sessions, nil
}

// StartCleanup waits for shutdown because Redis handles TTL expiration.
func (m *RedisManager) StartCleanup(ctx context.Context, interval time.Duration) {
	<-ctx.Done()
}

// Close releases resources held by the Redis client.
func (m *RedisManager) Close() error {
	return m.client.Close()
}

func (m *RedisManager) persist(session *Session) error {
	payload, err := m.marshalSession(session)
	if err != nil {
		return err
	}
	if err := m.client.Set(context.Background(), m.redisKey(session.Key), payload, m.ttl).Err(); err != nil {
		return fmt.Errorf("persist session to redis: %w", err)
	}
	return nil
}

func (m *RedisManager) redisKey(key SessionKey) string {
	return redisSessionKeyPrefix + key.String()
}

func (m *RedisManager) marshalSession(session *Session) (string, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}
	return string(payload), nil
}

func (m *RedisManager) unmarshalSession(payload string) (*Session, error) {
	var sess Session
	if err := json.Unmarshal([]byte(payload), &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	if sess.DispatchHistory == nil {
		sess.DispatchHistory = make([]DispatchRecord, 0)
	}
	return &sess, nil
}
