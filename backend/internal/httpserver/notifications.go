package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/notification"
)

func registerNotificationRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *notification.Service, logger *slog.Logger) {
	routes := api.Group("/notifications", requireAuth)
	routes.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		category := c.Query("category")
		if !notification.ValidateCategory(category) {
			writeAPIError(c, http.StatusBadRequest, "invalid_category", "消息分类不正确")
			return
		}
		result, summary, err := service.Search(c.Request.Context(), mustPrincipal(c), notification.SearchInput{
			UnreadOnly: c.Query("unread_only") == "true" || c.Query("unread_only") == "1",
			Category:   category,
			Page:       page,
		})
		if err != nil {
			logger.Error("list notifications", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取消息失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": result.Items, "total": result.Total, "page": result.Page, "page_size": result.PageSize, "summary": summary})
	})

	routes.GET("/summary", func(c *gin.Context) {
		result, err := service.Summary(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("load notification summary", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取消息汇总失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"summary": result})
	})
	routes.POST("/read", func(c *gin.Context) {
		var input struct {
			Key string `json:"key"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "消息标记参数不正确")
			return
		}
		if err := service.MarkRead(c.Request.Context(), mustPrincipal(c), input.Key); err != nil {
			var validation *notification.ValidationError
			switch {
			case errors.As(err, &validation):
				writeAPIError(c, http.StatusBadRequest, "invalid_request", validation.Message)
			case errors.Is(err, notification.ErrInvalid):
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "消息标记参数不正确")
			case errors.Is(err, notification.ErrNotFound):
				writeAPIError(c, http.StatusNotFound, "not_found", "消息不存在或已不在当前权限范围内")
			default:
				logger.Error("mark notification read", "error", err, "request_id", c.GetString("request_id"))
				writeAPIError(c, http.StatusInternalServerError, "internal_error", "标记消息失败")
			}
			return
		}
		c.Status(http.StatusNoContent)
	})
	routes.POST("/read-all", func(c *gin.Context) {
		if err := service.MarkAllRead(c.Request.Context(), mustPrincipal(c)); err != nil {
			logger.Error("mark all notifications read", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "全部标记已读失败")
			return
		}
		c.Status(http.StatusNoContent)
	})
}
