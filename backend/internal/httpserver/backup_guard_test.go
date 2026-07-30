package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/backup"
)

func TestWriteMaintenanceMiddlewareTreatsQualityScanAsWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gate := &backup.WriteGate{}
	if !gate.TryBeginBackup() {
		t.Fatal("failed to acquire backup gate")
	}
	defer gate.EndBackup()

	router := gin.New()
	router.Use(writeMaintenanceMiddleware(gate))
	router.GET("/api/v1/data-quality-rules/scan", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/data-quality-rules/scan", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestWriteMaintenanceMiddlewareKeepsOrdinaryGetReadableDuringBackup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gate := &backup.WriteGate{}
	if !gate.TryBeginBackup() {
		t.Fatal("failed to acquire backup gate")
	}
	defer gate.EndBackup()

	router := gin.New()
	router.Use(writeMaintenanceMiddleware(gate))
	router.GET("/api/v1/assets", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}
