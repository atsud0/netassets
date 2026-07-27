package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"netassets/internal/auditlog"
)

func registerAuditLogRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *auditlog.Service, logger *slog.Logger) {
	group := api.Group("/audit-logs", requireAuth)
	group.GET("/options", func(c *gin.Context) {
		options, err := service.Options(c.Request.Context(), mustPrincipal(c))
		switch {
		case errors.Is(err, auditlog.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以查看审计日志")
		case err != nil:
			logger.Error("list audit log options", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取审计筛选项失败")
		default:
			c.JSON(http.StatusOK, gin.H{"options": options})
		}
	})
	group.GET("", func(c *gin.Context) {
		page, err := parsePositiveInt(c.Query("page"), auditlog.DefaultPage)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "分页参数不正确")
			return
		}
		pageSize, err := parsePositiveInt(c.Query("page_size"), auditlog.DefaultPageSize)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "分页参数不正确")
			return
		}
		startAt, err := auditlog.ParseTime(c.Query("start_at"), false)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "开始时间格式不正确")
			return
		}
		endAt, err := auditlog.ParseTime(c.Query("end_at"), true)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "结束时间格式不正确")
			return
		}
		result, err := service.List(c.Request.Context(), mustPrincipal(c), auditlog.Filter{
			Page:           page,
			PageSize:       pageSize,
			Action:         c.Query("action"),
			ResourceType:   c.Query("resource_type"),
			Result:         c.Query("result"),
			UserID:         c.Query("user_id"),
			OrganizationID: c.Query("organization_id"),
			Keyword:        c.Query("q"),
			StartAt:        startAt,
			EndAt:          endAt,
		})
		switch {
		case errors.Is(err, auditlog.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以查看审计日志")
		case errors.Is(err, auditlog.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "审计日志筛选条件不正确")
		case err != nil:
			logger.Error("list audit logs", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取审计日志失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
}

func parsePositiveInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, auditlog.ErrInvalid
	}
	return parsed, nil
}
