package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/securityworkorder"
)

func TestSecurityBatchHTTPContract(test *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name     string
		failure  error
		expected int
	}{
		{"partial success", nil, 200}, {"forbidden", securityworkorder.ErrForbidden, 403},
		{"invalid", securityworkorder.ErrInvalid, 400},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			router := gin.New()
			group := router.Group("/api/v1", func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{}) })
			registerSecurityBatchRoute(group, "POST", "/batch", func(_ context.Context, _ auth.Principal, input securityworkorder.BatchInput, _, _ string) (securityworkorder.BatchResult, error) {
				if len(input.Items) != 2 {
					test.Fatal("batch input was not preserved")
				}
				return securityworkorder.BatchResult{Succeeded: 1, Failed: 1, Items: []securityworkorder.BatchItemResult{{ID: "one", Success: true}, {ID: "two", Code: "invalid_state", Message: "状态已变更"}}}, testCase.failure
			}, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
			request := httptest.NewRequest("POST", "/api/v1/batch", strings.NewReader(`{"items":[{"id":"one"},{"id":"two"}]}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != testCase.expected {
				test.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if response.Code == 200 {
				var result securityworkorder.BatchResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					test.Fatal(err)
				}
				if result.Succeeded != 1 || result.Failed != 1 || result.Items[1].Code != "invalid_state" {
					test.Fatalf("lost item results: %+v", result)
				}
			}
		})
	}
}

func TestSecurityBatchAndCandidateDetailRoutesRequirePermission(test *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1")
	registerSecurityWorkOrderRoutes(group, func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{}) }, &securityworkorder.Service{}, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	for _, route := range []struct{ method, path string }{
		{"PATCH", "/security-findings/batch"},
		{"POST", "/security-work-orders/batch/cancel"},
		{"POST", "/security-findings/import-candidates/batch/ignore"},
		{"GET", "/security-findings/import-candidates/00000000-0000-0000-0000-000000000001"},
	} {
		request := httptest.NewRequest(route.method, "/api/v1"+route.path, strings.NewReader(`{"items":[{"id":"test"}]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 403 {
			test.Fatalf("%s %s: %d %s", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestFindingSeverityRejectsInvalidFilter(test *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerSecurityWorkOrderRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{}) }, &securityworkorder.Service{}, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/security-findings?severity=urgent", nil))
	if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_filter") || !strings.Contains(response.Body.String(), "漏洞级别") {
		test.Fatalf("invalid severity response: %d %s", response.Code, response.Body.String())
	}
}

func TestDashboardDrilldownRejectsInvalidFilters(test *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerSecurityWorkOrderRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{}) }, &securityworkorder.Service{}, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	for _, path := range []string{
		"/security-findings?dispatch_state=unknown", "/security-findings?discovered_date=2026-02-30",
		"/security-findings?dispatched_date=2026-1-01", "/security-findings?closed_date=2026-11-03T00:00:00Z",
		"/security-findings?test_batch_only=unknown", "/security-findings?dashboard_scope=unknown", "/security-work-orders?dashboard_scope=unknown",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1"+path, nil))
		if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_filter") {
			test.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
