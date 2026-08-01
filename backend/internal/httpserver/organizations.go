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
	group.GET("/tree", func(c *gin.Context) {
		items, err := service.Tree(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("load organization tree", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取组织树失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": countOrganizationTree(items)})
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
	group.GET("/:id/impact", func(c *gin.Context) {
		item, err := service.GetImpact(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if writeOrganizationLifecycleError(c, logger, "load organization impact", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("/:id/disable", func(c *gin.Context) {
		var input organization.DisableInput
		if c.Request.ContentLength > 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "停用确认参数不正确")
				return
			}
		}
		item, err := service.Disable(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, organization.ErrBlocked):
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "organization_disable_blocked", "message": "请先处理组织的下级组织和未完成事项"}, "item": item})
		case errors.Is(err, organization.ErrConfirmationRequired):
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "organization_resource_confirmation_required", "message": "该组织仍有关联用户或台账，请确认影响后再停用"}, "item": item})
		case writeOrganizationLifecycleError(c, logger, "disable organization", err):
			return
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})
	group.POST("/:id/enable", func(c *gin.Context) {
		err := service.Enable(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, organization.ErrBlocked) {
			writeAPIError(c, http.StatusConflict, "organization_enable_blocked", "请先恢复并启用其上级组织")
			return
		}
		if writeOrganizationLifecycleError(c, logger, "enable organization", err) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.PATCH("/:id/structure", func(c *gin.Context) {
		var input organization.StructureInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "组织层级与类型格式不正确")
			return
		}
		item, err := service.UpdateStructure(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, organization.ErrConflict):
			writeAPIError(c, http.StatusConflict, "organization_hierarchy_cycle", "不能将组织迁移到自身或其下级组织")
		case errors.Is(err, organization.ErrBlocked):
			writeAPIError(c, http.StatusConflict, "organization_structure_blocked", "集团根组织不能调整，且新上级组织必须处于启用状态")
		case writeOrganizationLifecycleError(c, logger, "update organization structure", err):
			return
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})
	group.DELETE("/:id", func(c *gin.Context) {
		item, err := service.SoftDelete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if errors.Is(err, organization.ErrBlocked) {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "organization_delete_blocked", "message": "仅允许将已停用且无业务关联的空组织移入回收站"}, "item": item})
			return
		}
		if writeOrganizationLifecycleError(c, logger, "delete organization", err) {
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func writeOrganizationLifecycleError(c *gin.Context, logger *slog.Logger, operation string, err error) bool {
	switch {
	case errors.Is(err, organization.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以管理组织生命周期")
	case errors.Is(err, organization.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_organization", "组织标识或请求参数不正确")
	case errors.Is(err, organization.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "organization_not_found", "组织不存在或已在回收站")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "组织操作失败")
	default:
		return false
	}
	return true
}

func countOrganizationTree(items []organization.Organization) int {
	total := 0
	for _, item := range items {
		total++
		total += countOrganizationTree(item.Children)
	}
	return total
}
