package httpserver

import (
	"errors"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"netassets/internal/customfield"
	"strings"
)

func registerCustomFieldRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, svc *customfield.Service, logger *slog.Logger) {
	g := api.Group("/custom-fields", requireAuth)
	g.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.Search(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("resource_type")), page)
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	g.GET("/fillable", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.SearchFillableDefinitions(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("resource_type")), page)
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	g.PATCH("/:id", func(c *gin.Context) {
		var in customfield.DefinitionInput
		if err := c.ShouldBindJSON(&in); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查自定义字段配置")
			return
		}
		item, err := svc.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), in, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	g.POST("", func(c *gin.Context) {
		var in customfield.DefinitionInput
		if err := c.ShouldBindJSON(&in); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查自定义字段配置")
			return
		}
		item, err := svc.Create(c.Request.Context(), mustPrincipal(c), in, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeCustomFieldError(c, logger, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
}
func writeCustomFieldError(c *gin.Context, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, customfield.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以配置自定义字段")
	case errors.Is(err, customfield.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "custom_field_not_found", "自定义字段不存在")
	case errors.Is(err, customfield.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_custom_field", "请检查字段编码、名称、类型、选项及排序")
	case errors.Is(err, customfield.ErrConflict):
		writeAPIError(c, http.StatusConflict, "field_key_exists", "该对象类型下字段编码已存在")
	case errors.Is(err, customfield.ErrVersionRequired):
		writeAPIError(c, http.StatusBadRequest, "expected_version_required", "保存资产或业务系统自定义字段需要提供 expected_version")
	case errors.Is(err, customfield.ErrVersionConflict):
		writeAPIError(c, http.StatusConflict, "formal_version_conflict", "业务系统已被其他人修改，请刷新后重试")
	default:
		logger.Error("custom field", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理自定义字段失败")
	}
}
