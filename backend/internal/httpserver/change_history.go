package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/changehistory"
)

func registerChangeHistoryRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *changehistory.Service, logger *slog.Logger) {
	api.GET("/assets/:id/change-history", requireAuth, func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchAsset(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		switch {
		case errors.Is(err, changehistory.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该资产的变更历史")
		case errors.Is(err, changehistory.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "asset_not_found", "网络资产不存在")
		case errors.Is(err, changehistory.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_asset_id", "资产编号不正确")
		case err != nil:
			logger.Error("list asset change history", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取资产变更历史失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})

	api.GET("/business-systems/:id/change-history", requireAuth, func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchBusinessSystem(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		switch {
		case errors.Is(err, changehistory.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该业务系统的变更历史")
		case errors.Is(err, changehistory.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "business_system_not_found", "业务系统不存在")
		case errors.Is(err, changehistory.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_business_system_id", "业务系统编号不正确")
		case err != nil:
			logger.Error("list business system change history", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取业务系统变更历史失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
}
