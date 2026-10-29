package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/assetobservation"
)

func registerAssetObservationRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *assetobservation.Service, logger *slog.Logger) {
	group := api.Group("/assets", requireAuth)
	group.GET("/:id/external-observations", func(c *gin.Context) {
		items, err := service.List(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeAssetObservationError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	})
	group.POST("/:id/external-observations/refresh", func(c *gin.Context) {
		result, err := service.Refresh(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAssetObservationError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
}

func writeAssetObservationError(c *gin.Context, logger *slog.Logger, err error) {
	var inputError *assetobservation.InputValidationError
	switch {
	case errors.Is(err, assetobservation.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看或刷新该资产的外部信息")
	case errors.Is(err, assetobservation.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "asset_not_found", "网络资产不存在或尚未发布到正式台账")
	case errors.As(err, &inputError):
		writeAPIError(c, http.StatusBadRequest, "invalid_asset_id", inputError.Message)
	case errors.Is(err, assetobservation.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_asset_id", "资产编号不正确")
	case errors.Is(err, assetobservation.ErrUnsupported):
		writeAPIError(c, http.StatusUnprocessableEntity, "external_observation_unsupported", "当前资产类型不支持域名或证书信息采集")
	default:
		logger.Error("asset external observation", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取或刷新资产外部信息失败")
	}
}
