package agent

import (
	"fmt"
	"strings"
)

const defaultHealthRepository = "default"

// ServiceHealthStatus describes the health of a single downstream service.
type ServiceHealthStatus struct {
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
}

// RepositoryHealthStatus describes the health of the services used for a repository.
type RepositoryHealthStatus struct {
	Healthy         bool                `json:"healthy"`
	OpenCode        ServiceHealthStatus `json:"opencode"`
	WorktreeManager ServiceHealthStatus `json:"worktreeManager"`
}

// HealthReport contains the aggregated health across all configured repositories.
type HealthReport struct {
	Healthy      bool                              `json:"healthy"`
	Repositories map[string]RepositoryHealthStatus `json:"repositories"`
}

// Err converts an unhealthy report into an error with per-repository details.
func (r HealthReport) Err() error {
	if r.Healthy {
		return nil
	}

	failures := make([]string, 0, len(r.Repositories))
	for repoKey, repoStatus := range r.Repositories {
		if repoStatus.Healthy {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %s", repoKey, repoStatus.summary()))
	}
	if len(failures) == 0 {
		return fmt.Errorf("health check reported unhealthy state")
	}

	return fmt.Errorf(strings.Join(failures, "; "))
}

func (s RepositoryHealthStatus) summary() string {
	failures := make([]string, 0, 2)
	if !s.OpenCode.Healthy {
		failures = append(failures, "opencode: "+serviceStatusMessage(s.OpenCode))
	}
	if !s.WorktreeManager.Healthy {
		failures = append(failures, "worktree-manager: "+serviceStatusMessage(s.WorktreeManager))
	}
	if len(failures) == 0 {
		return "unhealthy response"
	}

	return strings.Join(failures, ", ")
}

func serviceStatusMessage(status ServiceHealthStatus) string {
	if status.Error != "" {
		return status.Error
	}
	return "unhealthy response"
}
