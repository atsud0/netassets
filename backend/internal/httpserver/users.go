package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	userdomain "netassets/internal/user"
)

func registerUserRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *userdomain.Service, logger *slog.Logger) {
	group := api.Group("/users", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePositiveInteger(c.Query("page"), 1)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "页码必须为正整数")
			return
		}
		pageSize, err := parsePositiveInteger(c.Query("page_size"), 20)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "每页数量必须为正整数")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), userdomain.ListQuery{
			Keyword:        c.Query("keyword"),
			Status:         c.Query("status"),
			OrganizationID: c.Query("organization_id"),
			RoleID:         c.Query("role_id"),
			Page:           page,
			PageSize:       pageSize,
		})
		if errors.Is(err, userdomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看用户")
			return
		}
		if errors.Is(err, userdomain.ErrInvalid) {
			var inputErr *userdomain.InputValidationError
			if errors.As(err, &inputErr) {
				writeAPIError(c, http.StatusBadRequest, "invalid_query", inputErr.Error())
				return
			}
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "用户筛选条件不正确")
			return
		}
		if err != nil {
			logger.Error("list users", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取用户失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": result.Items, "total": result.Total, "page": page, "page_size": pageSize})
	})
	group.GET("/manageable-organizations", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchManageableOrganizations(c.Request.Context(), mustPrincipal(c), page)
		if errors.Is(err, userdomain.ErrForbidden) {
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权管理组织用户")
			return
		}
		if err != nil {
			logger.Error("list manageable organizations", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取可管理组织失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		writeUserResult(c, logger, err, item, http.StatusOK, "读取用户详情失败")
	})
	group.POST("", func(c *gin.Context) {
		var input userdomain.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "用户信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeUserResult(c, logger, err, item, http.StatusCreated, "新增用户失败")
	})
	group.PUT("/:id", func(c *gin.Context) {
		var input userdomain.UpdateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "用户信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeUserResult(c, logger, err, item, http.StatusOK, "更新用户失败")
	})
	group.PATCH("/:id/status", func(c *gin.Context) {
		var input userdomain.StatusInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "用户状态格式不正确")
			return
		}
		err := service.UpdateStatus(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeUserActionResult(c, logger, err, "更新用户状态失败")
	})
	group.POST("/:id/reset-password", func(c *gin.Context) {
		var input userdomain.ResetPasswordInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "密码格式不正确")
			return
		}
		err := service.ResetPassword(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeUserActionResult(c, logger, err, "重置密码失败")
	})
}

func parsePositiveInteger(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, userdomain.ErrInvalid
	}
	return number, nil
}

func writeUserResult(c *gin.Context, logger *slog.Logger, err error, item userdomain.User, success int, fallback string) {
	if err == nil {
		c.JSON(success, gin.H{"item": item})
		return
	}
	writeUserError(c, logger, err, fallback)
}
func writeUserActionResult(c *gin.Context, logger *slog.Logger, err error, fallback string) {
	if err == nil {
		c.Status(http.StatusNoContent)
		return
	}
	writeUserError(c, logger, err, fallback)
}
func writeUserError(c *gin.Context, logger *slog.Logger, err error, fallback string) {
	var inputErr *userdomain.InputValidationError
	switch {
	case errors.Is(err, userdomain.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权管理用户")
	case errors.As(err, &inputErr):
		writeAPIError(c, http.StatusBadRequest, "invalid_user", inputErr.Error())
	case errors.Is(err, userdomain.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_user", "请检查用户资料、密码、任职组织、角色和数据范围")
	case errors.Is(err, userdomain.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "user_or_organization_not_found", "用户、组织或角色不存在")
	case errors.Is(err, userdomain.ErrConflict):
		writeAPIError(c, http.StatusConflict, "username_exists", "用户名已存在")
	default:
		logger.Error("write user", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", fallback)
	}
}
