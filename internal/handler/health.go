// Package handler provides HTTP handlers for the application.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openagent/github-bridge/internal/agent"
)

const defaultHealthTimeout = 5 * time.Second

type healthReporter interface {
	HealthStatus(ctx context.Context) agent.HealthReport
}

// HealthHandler serves the bridge health endpoint.
type HealthHandler struct {
	reporter healthReporter
	version  string
	timeout  time.Duration
}

type healthResponse struct {
	Healthy  bool           `json:"healthy"`
	Status   string         `json:"status"`
	Version  string         `json:"version"`
	Services healthServices `json:"services"`
}

type healthServices struct {
	Bridge       agent.ServiceHealthStatus               `json:"bridge"`
	Repositories map[string]agent.RepositoryHealthStatus `json:"repositories,omitempty"`
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(reporter healthReporter, version string) *HealthHandler {
	return &HealthHandler{
		reporter: reporter,
		version:  version,
		timeout:  defaultHealthTimeout,
	}
}

// HandleHealth returns the bridge health and its downstream dependency states.
func (h *HealthHandler) HandleHealth(c *gin.Context) {
	response := healthResponse{
		Healthy: true,
		Status:  "healthy",
		Version: h.version,
		Services: healthServices{
			Bridge:       agent.ServiceHealthStatus{Healthy: true},
			Repositories: make(map[string]agent.RepositoryHealthStatus),
		},
	}

	if h.reporter == nil {
		response.Healthy = false
		response.Status = "unhealthy"
		response.Services.Bridge = agent.ServiceHealthStatus{
			Error: "agent health reporter is not configured",
		}
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	report := h.reporter.HealthStatus(ctx)
	response.Services.Repositories = report.Repositories
	response.Healthy = response.Services.Bridge.Healthy && report.Healthy
	if !response.Healthy {
		response.Status = "unhealthy"
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	c.JSON(http.StatusOK, response)
}
