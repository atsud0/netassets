package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/networkarea"
)

func registerNetworkAreaRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *networkarea.Service, logger *slog.Logger) {
	group := api.Group("/network-areas", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("organization_id")), page)
		switch {
		case errors.Is(err, networkarea.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该组织的网络区域")
		case errors.Is(err, networkarea.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_network_area", networkAreaInputMessage(err, "网络区域筛选条件不正确"))
		case err != nil:
			logger.Error("list network areas", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取网络区域失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.POST("", func(c *gin.Context) {
		var input networkarea.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "网络区域信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, networkarea.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权维护该组织的网络区域")
		case errors.Is(err, networkarea.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_network_area", networkAreaInputMessage(err, "请检查所属组织、名称、状态和排序"))
		case errors.Is(err, networkarea.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "所属组织不存在")
		case errors.Is(err, networkarea.ErrConflict):
			writeAPIError(c, http.StatusConflict, "network_area_exists", "该组织下已存在同名网络区域")
		case err != nil:
			logger.Error("create network area", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "新增网络区域失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})
	group.PATCH("/:id", func(c *gin.Context) {
		var input networkarea.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "网络区域信息格式不正确")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, networkarea.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权维护该组织的网络区域")
		case errors.Is(err, networkarea.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_network_area", networkAreaInputMessage(err, "请检查网络区域信息；所属组织不可直接变更"))
		case errors.Is(err, networkarea.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "network_area_not_found", "网络区域不存在")
		case errors.Is(err, networkarea.ErrConflict):
			writeAPIError(c, http.StatusConflict, "network_area_exists", "该组织下已存在同名网络区域")
		case err != nil:
			logger.Error("update network area", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "编辑网络区域失败")
		default:
			c.JSON(http.StatusOK, gin.H{"item": item})
		}
	})
}
