package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/recyclebin"
)

func registerRecycleBinRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *recyclebin.Service, logger *slog.Logger) {
	group := api.Group("/recycle-bin", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page, c.Query("resource_type"))
		switch {
		case errors.Is(err, recyclebin.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可查看回收站")
		case errors.Is(err, recyclebin.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "资源类型仅支持 asset、business_system 或 organization")
		case err != nil:
			logger.Error("list recycle bin", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取回收站失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})

	group.POST("/assets/:id/restore", func(c *gin.Context) {
		err := service.RestoreAsset(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		handleRecycleBinRestoreError(c, err, logger, "网络资产")
	})
	group.POST("/business-systems/:id/restore", func(c *gin.Context) {
		err := service.RestoreBusinessSystem(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		handleRecycleBinRestoreError(c, err, logger, "业务系统")
	})
	group.POST("/organizations/:id/restore", func(c *gin.Context) {
		err := service.RestoreOrganization(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		handleRecycleBinRestoreError(c, err, logger, "组织机构")
	})
}

func handleRecycleBinRestoreError(c *gin.Context, err error, logger *slog.Logger, resourceName string) {
	switch {
	case errors.Is(err, recyclebin.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可执行恢复操作")
	case errors.Is(err, recyclebin.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_resource_id", resourceName+"编号不正确")
	case errors.Is(err, recyclebin.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "recycle_bin_item_not_found", resourceName+"不存在或已恢复")
	case errors.Is(err, recyclebin.ErrConflict):
		writeAPIError(c, http.StatusConflict, "active_resource_conflict", "存在相同的在用资源，请先处理重复数据后再恢复")
	case err != nil:
		logger.Error("restore recycle bin resource", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "恢复"+resourceName+"失败")
	default:
		c.Status(http.StatusNoContent)
	}
}
