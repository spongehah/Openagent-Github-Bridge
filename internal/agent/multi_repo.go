// Package agent provides multi-repository support for OpenCode.
//
// MultiRepoOpenCodeAdapter routes tasks to the correct OpenCode instance
// based on the repository owner/name mapping in configuration.
//
// Architecture:
// - Each repository maps to a dedicated OpenCode instance
// - OpenCode instances must be started in the corresponding repo directory
// - Tasks are routed based on {owner}/{repo} key
//
// Reference: https://open-code.ai/docs/en/server
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/openagent/github-bridge/internal/config"
)

// MultiRepoOpenCodeAdapter routes tasks to repo-specific OpenCode instances.
//
// It maintains a pool of OpenCodeAdapter instances, one per repository.
// New adapters are created lazily when a repository is first accessed.
type MultiRepoOpenCodeAdapter struct {
	config   *config.Config
	adapters map[string]*OpenCodeAdapter // key: "owner/repo"
	mu       sync.RWMutex
}

// NewMultiRepoOpenCodeAdapter creates a new multi-repo adapter.
//
// Usage modes:
// - Single repo: If no repositories are configured, uses default OpenCode config
// - Multi repo: Routes to repo-specific OpenCode instances based on config
func NewMultiRepoOpenCodeAdapter(cfg *config.Config) *MultiRepoOpenCodeAdapter {
	return &MultiRepoOpenCodeAdapter{
		config:   cfg,
		adapters: make(map[string]*OpenCodeAdapter),
	}
}

// getOrCreateAdapter returns an adapter for the given repository.
// Creates a new adapter if one doesn't exist.
func (m *MultiRepoOpenCodeAdapter) getOrCreateAdapter(owner, repo string) *OpenCodeAdapter {
	repoKey := owner + "/" + repo

	// Fast path: check if adapter exists
	m.mu.RLock()
	adapter, exists := m.adapters[repoKey]
	m.mu.RUnlock()

	if exists {
		return adapter
	}

	// Slow path: create new adapter
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if adapter, exists = m.adapters[repoKey]; exists {
		return adapter
	}

	// Get repo-specific or default config
	openCodeCfg := m.config.GetOpenCodeConfigForRepo(owner, repo)
	adapter = NewOpenCodeAdapter(openCodeCfg)
	m.adapters[repoKey] = adapter

	fmt.Printf("[MultiRepo] Created OpenCode adapter for %s -> %s\n", repoKey, openCodeCfg.Host)

	return adapter
}

// DispatchTask routes the task to the appropriate OpenCode instance.
func (m *MultiRepoOpenCodeAdapter) DispatchTask(ctx context.Context, task TaskContext) (*DispatchResult, error) {
	adapter := m.getOrCreateAdapter(task.RepoOwner, task.RepoName)
	return adapter.DispatchTask(ctx, task)
}

// HealthCheck checks the health of all configured OpenCode instances.
// Returns error if any instance is unhealthy.
func (m *MultiRepoOpenCodeAdapter) HealthCheck(ctx context.Context) error {
	return m.HealthStatus(ctx).Err()
}

// HealthStatus returns structured health details for all configured repositories.
func (m *MultiRepoOpenCodeAdapter) HealthStatus(ctx context.Context) HealthReport {
	report := HealthReport{
		Healthy:      true,
		Repositories: make(map[string]RepositoryHealthStatus),
	}

	repoKeys := m.GetConfiguredRepos()
	sort.Strings(repoKeys)

	if len(repoKeys) == 0 {
		defaultStatus := NewOpenCodeAdapter(m.config.OpenCode).repositoryHealthStatus(ctx)
		report.Healthy = defaultStatus.Healthy
		report.Repositories[defaultHealthRepository] = defaultStatus
		return report
	}

	for _, repoKey := range repoKeys {
		owner, repo, ok := strings.Cut(repoKey, "/")
		if !ok || owner == "" || repo == "" {
			report.Healthy = false
			report.Repositories[repoKey] = RepositoryHealthStatus{
				OpenCode: ServiceHealthStatus{
					Error: "invalid repository key",
				},
				WorktreeManager: ServiceHealthStatus{
					Error: "invalid repository key",
				},
			}
			continue
		}

		repositoryStatus := m.getOrCreateAdapter(owner, repo).repositoryHealthStatus(ctx)
		report.Repositories[repoKey] = repositoryStatus
		if !repositoryStatus.Healthy {
			report.Healthy = false
		}
	}

	return report
}

// GetConfiguredRepos returns a list of explicitly configured repositories.
func (m *MultiRepoOpenCodeAdapter) GetConfiguredRepos() []string {
	repos := make([]string, 0, len(m.config.Repositories))
	for repo := range m.config.Repositories {
		repos = append(repos, repo)
	}
	return repos
}

// IsMultiRepoMode returns true if multiple repositories are configured.
func (m *MultiRepoOpenCodeAdapter) IsMultiRepoMode() bool {
	return len(m.config.Repositories) > 0
}
