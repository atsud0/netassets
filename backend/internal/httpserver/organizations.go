package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/organization"
)

func registerOrganizationRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *organization.Service, logger *slog.Logger) {
	group := api.Group("/organizations", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list organizations", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取组织机构失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("", func(c *gin.Context) {
		var input organization.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "组织信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, organization.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权新增组织")
		case errors.Is(err, organization.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_organization", "请检查组织编码、名称、类型和上级组织")
		case errors.Is(err, organization.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "parent_not_found", "上级组织不存在")
		case errors.Is(err, organization.ErrConflict):
			writeAPIError(c, http.StatusConflict, "organization_code_exists", "组织编码已存在")
		case err != nil:
			logger.Error("create organization", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增组织失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})
	group.PATCH("/:id", func(c *gin.Context) {
		var input organization.UpdateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "组织信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, organization.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权编辑组织信息")
		case errors.Is(err, organization.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_organization", "请检查组织名称及基本信息")
		case errors.Is(err, organization.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "组织机构不存在")
		case err != nil:
			logger.Error("update organization", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "编辑组织机构失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})

}
