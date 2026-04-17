package config

import "testing"

func TestValidateRejectsUnsupportedSessionStorage(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		GitHub: GitHubConfig{
			WebhookSecret: "secret",
			Token:         "token",
		},
		Session: SessionConfig{
			Storage: "sqlite",
			TTL:     1,
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unsupported session storage")
	}
}

func TestValidateRejectsRedisStorageWithoutAddr(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		GitHub: GitHubConfig{
			WebhookSecret: "secret",
			Token:         "token",
		},
		Session: SessionConfig{
			Storage: "redis",
			TTL:     1,
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing redis addr")
	}
}
