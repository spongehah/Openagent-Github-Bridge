package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openagent/github-bridge/internal/agent"
)

type stubHealthReporter struct {
	report agent.HealthReport
}

func (s stubHealthReporter) HealthStatus(ctx context.Context) agent.HealthReport {
	return s.report
}

func TestHealthHandlerReturnsHealthyResponse(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/health", NewHealthHandler(stubHealthReporter{
		report: agent.HealthReport{
			Healthy: true,
			Repositories: map[string]agent.RepositoryHealthStatus{
				"default": {
					Healthy: true,
					OpenCode: agent.ServiceHealthStatus{
						Healthy: true,
						Version: "1.0.0",
					},
					WorkspaceManager: agent.ServiceHealthStatus{
						Healthy: true,
					},
				},
			},
		},
	}, "1.0.0").HandleHealth)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", recorder.Code)
	}

	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !response.Healthy || response.Status != "healthy" {
		t.Fatalf("unexpected response status: %#v", response)
	}
	if !response.Services.Bridge.Healthy {
		t.Fatalf("expected bridge to be healthy, got %#v", response.Services.Bridge)
	}
	if !response.Services.Repositories["default"].WorkspaceManager.Healthy {
		t.Fatalf("expected workspace-manager to be healthy, got %#v", response.Services.Repositories["default"])
	}
}

func TestHealthHandlerReturns503WhenDependencyIsUnhealthy(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/health", NewHealthHandler(stubHealthReporter{
		report: agent.HealthReport{
			Healthy: false,
			Repositories: map[string]agent.RepositoryHealthStatus{
				"default": {
					Healthy: false,
					OpenCode: agent.ServiceHealthStatus{
						Healthy: true,
						Version: "1.0.0",
					},
					WorkspaceManager: agent.ServiceHealthStatus{
						Error: "timeout",
					},
				},
			},
		},
	}, "1.0.0").HandleHealth)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 status, got %d", recorder.Code)
	}

	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if response.Healthy || response.Status != "unhealthy" {
		t.Fatalf("unexpected response status: %#v", response)
	}
	if response.Services.Repositories["default"].WorkspaceManager.Error != "timeout" {
		t.Fatalf("expected dependency error in response, got %#v", response.Services.Repositories["default"])
	}
}
