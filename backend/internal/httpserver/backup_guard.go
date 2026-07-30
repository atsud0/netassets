package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/backup"
)

// writeMaintenanceMiddleware keeps regular application writes consistent with a
// Web-created backup. It does not queue writes: callers receive a clear retry
// response while the short backup window is active.
func writeMaintenanceMiddleware(gate *backup.WriteGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if (!isMutatingRequest(c.Request.Method) && !isDataQualityScanRequest(c.Request)) || isBackupCreationRequest(c.Request) {
			c.Next()
			return
		}
		if !gate.TryEnterWrite() {
			writeAPIError(c, http.StatusServiceUnavailable, "backup_in_progress", "系统正在创建备份，暂时不能执行写操作，请稍后重试")
			c.Abort()
			return
		}
		defer gate.LeaveWrite()
		c.Next()
	}
}

func isMutatingRequest(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isBackupCreationRequest(request *http.Request) bool {
	return request.Method == http.MethodPost && request.URL.Path == "/api/v1/backups"
}

func isDataQualityScanRequest(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.Path == "/api/v1/data-quality-rules/scan"
}
