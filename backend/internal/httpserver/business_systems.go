package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/businesssystem"
)

func registerBusinessSystemRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *businesssystem.Service, logger *slog.Logger) {
	group := api.Group("/business-systems", requireAuth)
	group.GET("", func(c *gin.Context) {
		items, err := service.List(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("list business systems", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
	})
	group.POST("", func(c *gin.Context) {
		var input businesssystem.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, businesssystem.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权在该组织新增业务系统")
		case errors.Is(err, businesssystem.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system", "请检查系统信息和数据安全属性")
		case errors.Is(err, businesssystem.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "所属组织不存在")
		case errors.Is(err, businesssystem.ErrConflict):
			writeAPIError(c, http.StatusConflict, "system_code_exists", "系统编码已存在")
		case err != nil:
			logger.Error("create business system", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增业务系统失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})
}
