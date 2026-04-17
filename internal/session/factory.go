package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/openagent/github-bridge/internal/config"
)

// NewManager creates a session manager from configuration.
func NewManager(ctx context.Context, cfg config.SessionConfig) (Manager, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Storage)) {
	case "", "memory":
		return NewMemoryManager(cfg.TTL), nil
	case "redis":
		return NewRedisManager(ctx, cfg.Redis, cfg.TTL)
	default:
		return nil, fmt.Errorf("unsupported session storage: %s", cfg.Storage)
	}
}
