package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/dashboard"
)

func registerDashboardRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *dashboard.Service, logger *slog.Logger) {
	api.GET("/dashboard/summary", requireAuth, func(c *gin.Context) {
		result, err := service.Get(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("load dashboard summary", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取工作台统计失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"summary": result})
	})

	api.GET("/dashboard/security-overview", requireAuth, func(c *gin.Context) {
		result, err := service.GetSecurityOverview(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("load security dashboard overview", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全治理统计失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"overview": result})
	})
}
