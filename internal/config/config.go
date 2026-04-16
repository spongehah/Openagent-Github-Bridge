// Package config provides configuration management for the application.
// It supports loading from YAML files and environment variables.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server       ServerConfig                `mapstructure:"server"`
	GitHub       GitHubConfig                `mapstructure:"github"`
	OpenCode     OpenCodeConfig              `mapstructure:"opencode"`     // Default OpenCode config (single repo mode)
	Repositories map[string]RepositoryConfig `mapstructure:"repositories"` // Multi-repo mode: repo -> OpenCode mapping
	Queue        QueueConfig                 `mapstructure:"queue"`
	Session      SessionConfig               `mapstructure:"session"`
	Log          LogConfig                   `mapstructure:"log"`
	Trigger      TriggerConfig               `mapstructure:"trigger"`
	Features     FeaturesConfig              `mapstructure:"features"`
}

// RepositoryConfig holds OpenCode configuration for a specific repository.
// Used in multi-repo mode where each repo has its own OpenCode instance.
type RepositoryConfig struct {
	OpenCodeHost            string `mapstructure:"opencode_host"`             // OpenCode server URL for this repo
	OpenCodeUsername        string `mapstructure:"opencode_username"`         // HTTP Basic Auth username (optional)
	OpenCodePassword        string `mapstructure:"opencode_password"`         // HTTP Basic Auth password (optional)
	WorktreeManagerHost     string `mapstructure:"worktree_manager_host"`     // Companion worktree-manager URL for this repo
	WorktreeManagerUsername string `mapstructure:"worktree_manager_username"` // HTTP Basic Auth username (optional)
	WorktreeManagerPassword string `mapstructure:"worktree_manager_password"` // HTTP Basic Auth password (optional)
}

// GetOpenCodeConfigForRepo returns the OpenCode configuration for a specific repository.
// If multi-repo mode is configured and the repo exists, returns repo-specific config.
// Otherwise, falls back to the default OpenCode config.
func (c *Config) GetOpenCodeConfigForRepo(owner, repo string) OpenCodeConfig {
	repoKey := owner + "/" + repo
	if repoConfig, exists := c.Repositories[repoKey]; exists {
		// Use repo-specific config, fall back to defaults for missing fields
		cfg := c.OpenCode
		if repoConfig.OpenCodeHost != "" {
			cfg.Host = repoConfig.OpenCodeHost
		}
		if repoConfig.OpenCodeUsername != "" {
			cfg.Username = repoConfig.OpenCodeUsername
		}
		if repoConfig.OpenCodePassword != "" {
			cfg.Password = repoConfig.OpenCodePassword
		}
		if repoConfig.WorktreeManagerHost != "" {
			cfg.WorktreeManagerHost = repoConfig.WorktreeManagerHost
		}
		if repoConfig.WorktreeManagerUsername != "" {
			cfg.WorktreeManagerUsername = repoConfig.WorktreeManagerUsername
		}
		if repoConfig.WorktreeManagerPassword != "" {
			cfg.WorktreeManagerPassword = repoConfig.WorktreeManagerPassword
		}
		return cfg
	}
	return c.OpenCode
}

// FeaturesConfig holds feature toggle configuration.
// Principle: Only events with actual triggered actions need an enable config.
type FeaturesConfig struct {
	AIFix    AIFixConfig    `mapstructure:"ai_fix"`    // Issue label trigger (ai-fix)
	PRReview PRReviewConfig `mapstructure:"pr_review"` // PR review features
}

// AIFixConfig configures the ai-fix label trigger feature.
type AIFixConfig struct {
	Enabled bool     `mapstructure:"enabled"` // Enable ai-fix label trigger
	Labels  []string `mapstructure:"labels"`  // Labels that trigger ai-fix (default: "ai-fix")
}

// PRReviewConfig configures the PR review features.
type PRReviewConfig struct {
	Enabled             bool     `mapstructure:"enabled"`               // Enable auto PR review on PR opened
	SkipDraftPRs        bool     `mapstructure:"skip_draft_prs"`        // Skip draft PRs (only for PR opened)
	SkipBotPRs          bool     `mapstructure:"skip_bot_prs"`          // Skip bot PRs (only for PR opened)
	LabelTriggerEnabled bool     `mapstructure:"label_trigger_enabled"` // Enable PR review via label (ai-review)
	Labels              []string `mapstructure:"labels"`                // Labels that trigger PR review (default: "ai-review")
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// GitHubConfig holds GitHub API and webhook configuration.
type GitHubConfig struct {
	WebhookSecret     string `mapstructure:"webhook_secret"`
	Token             string `mapstructure:"token"`
	AppID             string `mapstructure:"app_id"`
	AppPrivateKeyPath string `mapstructure:"app_private_key_path"`
	APIBaseURL        string `mapstructure:"api_base_url"`
}

// OpenCodeConfig holds OpenCode API configuration.
// Reference: https://open-code.ai/docs/en/server#authentication
type OpenCodeConfig struct {
	Host                    string        `mapstructure:"host"`
	Username                string        `mapstructure:"username"` // HTTP Basic Auth username (default: "opencode")
	Password                string        `mapstructure:"password"` // HTTP Basic Auth password (optional, set via OPENCODE_SERVER_PASSWORD)
	DefaultModel            string        `mapstructure:"default_model"`
	Timeout                 time.Duration `mapstructure:"timeout"`
	WorktreeManagerHost     string        `mapstructure:"worktree_manager_host"`     // Companion worktree-manager URL
	WorktreeManagerUsername string        `mapstructure:"worktree_manager_username"` // Companion worktree-manager HTTP Basic Auth username
	WorktreeManagerPassword string        `mapstructure:"worktree_manager_password"` // Companion worktree-manager HTTP Basic Auth password
}

// QueueConfig holds task queue configuration.
type QueueConfig struct {
	Workers    int `mapstructure:"workers"`
	BufferSize int `mapstructure:"buffer_size"`
}

// SessionConfig holds session management configuration.
type SessionConfig struct {
	Storage string        `mapstructure:"storage"`
	TTL     time.Duration `mapstructure:"ttl"`
	Redis   RedisConfig   `mapstructure:"redis"`
}

// RedisConfig holds Redis connection configuration.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// TriggerConfig holds trigger keyword configuration.
type TriggerConfig struct {
	Prefix           string   `mapstructure:"prefix"`
	RespondAllIssues bool     `mapstructure:"respond_all_issues"`
	Labels           []string `mapstructure:"labels"` // Labels that trigger AI (e.g., "ai-fix")
}

// Load reads configuration from file and environment variables.
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Configure viper
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found; rely on environment variables
	}

	// Environment variable support
	v.SetEnvPrefix("")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific environment variables
	bindEnvVars(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// setDefaults sets default configuration values.
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 7777)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")

	// GitHub defaults
	v.SetDefault("github.api_base_url", "https://api.github.com")

	// OpenCode defaults
	// Reference: https://open-code.ai/docs/en/server
	v.SetDefault("opencode.host", "http://localhost:4096") // Default OpenCode server port
	v.SetDefault("opencode.username", "opencode")          // Default username per OpenCode docs
	v.SetDefault("opencode.password", "")                  // Empty = no auth
	v.SetDefault("opencode.default_model", "anthropic/claude-sonnet-4-20250514")
	v.SetDefault("opencode.timeout", "120s")
	v.SetDefault("opencode.worktree_manager_host", "http://localhost:4081")
	v.SetDefault("opencode.worktree_manager_username", "worktree-manager")
	v.SetDefault("opencode.worktree_manager_password", "")

	// Queue defaults
	v.SetDefault("queue.workers", 5)
	v.SetDefault("queue.buffer_size", 100)

	// Session defaults
	v.SetDefault("session.storage", "memory")
	v.SetDefault("session.ttl", "24h")
	v.SetDefault("session.redis.addr", "localhost:6379")
	v.SetDefault("session.redis.db", 0)

	// Log defaults
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	// Trigger defaults
	v.SetDefault("trigger.prefix", "@ogb-bot")
	v.SetDefault("trigger.respond_all_issues", false)
	v.SetDefault("trigger.labels", []string{"ai-fix"})

	// Features defaults
	// Principle: Only events with actual triggered actions need an enable config.

	// AI-Fix: Issue label trigger (e.g., "ai-fix" label triggers auto-fix)
	v.SetDefault("features.ai_fix.enabled", true)
	v.SetDefault("features.ai_fix.labels", []string{"ai-fix"})

	// PR Review: Auto review on PR opened or labeled
	v.SetDefault("features.pr_review.enabled", false) // PR opened auto-review: disabled by default
	v.SetDefault("features.pr_review.skip_draft_prs", true)
	v.SetDefault("features.pr_review.skip_bot_prs", true)
	v.SetDefault("features.pr_review.label_trigger_enabled", true) // PR label trigger: enabled by default
	v.SetDefault("features.pr_review.labels", []string{"ai-review"})
}

// bindEnvVars binds environment variables to config keys.
func bindEnvVars(v *viper.Viper) {
	// GitHub
	_ = v.BindEnv("github.webhook_secret", "GITHUB_WEBHOOK_SECRET")
	_ = v.BindEnv("github.token", "GITHUB_TOKEN")
	_ = v.BindEnv("github.app_id", "GITHUB_APP_ID")
	_ = v.BindEnv("github.app_private_key_path", "GITHUB_APP_PRIVATE_KEY_PATH")

	// OpenCode
	// Reference: https://open-code.ai/docs/en/server#authentication
	_ = v.BindEnv("opencode.host", "OPENCODE_HOST")
	_ = v.BindEnv("opencode.username", "OPENCODE_SERVER_USERNAME")
	_ = v.BindEnv("opencode.password", "OPENCODE_SERVER_PASSWORD")
	_ = v.BindEnv("opencode.default_model", "OPENCODE_DEFAULT_MODEL")
	_ = v.BindEnv("opencode.worktree_manager_host", "WORKTREE_MANAGER_HOST")
	_ = v.BindEnv("opencode.worktree_manager_username", "WORKTREE_MANAGER_USERNAME")
	_ = v.BindEnv("opencode.worktree_manager_password", "WORKTREE_MANAGER_PASSWORD")

	// Redis
	_ = v.BindEnv("session.redis.addr", "REDIS_ADDR")
	_ = v.BindEnv("session.redis.password", "REDIS_PASSWORD")
}

// Validate checks if required configuration fields are set.
func (c *Config) Validate() error {
	if c.GitHub.WebhookSecret == "" {
		return fmt.Errorf("GITHUB_WEBHOOK_SECRET is required")
	}
	if c.GitHub.Token == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}
	return nil
}

// Address returns the server address string.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
