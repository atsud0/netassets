package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	userdomain "netassets/internal/user"
)

func registerUserRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *userdomain.Service, logger *slog.Logger) {
	group := api.Group("/users", requireAuth)
	group.GET("", func(c *gin.Context) {
		items, err := service.List(c.Request.Context(), mustPrincipal(c))
		if errors.Is(err, userdomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看用户")
			return
		}
		if err != nil {
			logger.Error("list users", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取用户失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
	})
	group.POST("", func(c *gin.Context) {
		var input userdomain.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "用户信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, userdomain.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权新增用户")
		case errors.Is(err, userdomain.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_user", "请检查用户名、密码、所属组织和角色")
		case errors.Is(err, userdomain.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "所属组织不存在")
		case errors.Is(err, userdomain.ErrConflict):
			writeAPIError(c, http.StatusConflict, "username_exists", "用户名已存在")
		case err != nil:
			logger.Error("create user", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增用户失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})
}
