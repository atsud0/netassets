package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	roledomain "netassets/internal/role"
)

func registerRoleRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *roledomain.Service, logger *slog.Logger) {
	group := api.Group("/roles", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page)
		if errors.Is(err, roledomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看角色配置")
			return
		}
		if err != nil {
			logger.Error("list roles", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取角色失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/assignable", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchAssignable(c.Request.Context(), mustPrincipal(c), page)
		if errors.Is(err, roledomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权为用户分配角色")
			return
		}
		if err != nil {
			logger.Error("list assignable roles", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取可分配角色失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/permissions", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchPermissions(c.Request.Context(), mustPrincipal(c), page)
		if errors.Is(err, roledomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看权限字典")
			return
		}
		if err != nil {
			logger.Error("list permissions", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取权限字典失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("", func(c *gin.Context) {
		var input roledomain.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "角色信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeRoleResult(c, logger, err, item, http.StatusCreated, "新增角色失败")
	})
	group.PUT("/:id", func(c *gin.Context) {
		var input roledomain.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "角色信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeRoleResult(c, logger, err, item, http.StatusOK, "更新角色失败")
	})
	group.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, roledomain.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "system_role_protected", "系统角色不可修改或删除")
		case errors.Is(err, roledomain.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "role_not_found", "角色不存在")
		case errors.Is(err, roledomain.ErrInUse):
			writeAPIError(c, http.StatusConflict, "role_in_use", "该角色已被任职使用，无法删除")
		case err != nil:
			logger.Error("delete role", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "删除角色失败")
		default:
			c.Status(http.StatusNoContent)
		}
	})
}

func writeRoleResult(c *gin.Context, logger *slog.Logger, err error, item roledomain.Role, success int, fallback string) {
	switch {
	case errors.Is(err, roledomain.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "system_role_protected", "系统角色不可修改，或当前账号无权操作")
	case errors.Is(err, roledomain.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_role", "请检查角色名称、编码、状态和权限项")
	case errors.Is(err, roledomain.ErrConflict):
		writeAPIError(c, http.StatusConflict, "role_key_exists", "角色编码已存在")
	case errors.Is(err, roledomain.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "role_not_found", "角色不存在")
	case err != nil:
		logger.Error("write role", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", fallback)
	default:
		c.JSON(success, gin.H{"item": item})
	}
}
