package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/assetcandidate"
	"netassets/internal/securityworkorder"
)

func registerAssetCandidateRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *assetcandidate.Service, findingService *securityworkorder.Service, logger *slog.Logger) {
	group := api.Group("/asset-candidates", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("status")), page)
		if err != nil {
			writeAssetCandidateError(c, logger, "list asset candidates", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeAssetCandidateError(c, logger, "get asset candidate", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("", func(c *gin.Context) {
		var input assetcandidate.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查待确认资产信息")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAssetCandidateError(c, logger, "create asset candidate", err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.POST("/:id/accept", func(c *gin.Context) {
		var input assetcandidate.AcceptInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请补充正式资产的入库信息")
			return
		}
		item, err := service.Accept(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAssetCandidateError(c, logger, "accept asset candidate", err)
			return
		}
		response := gin.H{"item": item}
		if findingService != nil {
			if linkErr := findingService.LinkAcceptedAssetCandidate(c.Request.Context(), mustPrincipal(c), c.Param("id"), item.ID, c.GetString("request_id"), c.ClientIP()); linkErr != nil {
				logger.Error("link imported findings after asset candidate acceptance", "error", linkErr, "asset_candidate_id", c.Param("id"), "asset_id", item.ID, "request_id", c.GetString("request_id"))
				response["warning"] = "资产已入库，但关联漏洞暂未全部生成安全发现，请刷新后重试"
			}
		}
		c.JSON(http.StatusOK, response)
	})
	group.POST("/:id/ignore", func(c *gin.Context) {
		var input assetcandidate.ReviewInput
		if c.Request.ContentLength != 0 && c.ShouldBindJSON(&input) != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查处理说明")
			return
		}
		if err := service.Ignore(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP()); err != nil {
			writeAssetCandidateError(c, logger, "ignore asset candidate", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/:id/invalidate", func(c *gin.Context) {
		var input assetcandidate.ReviewInput
		if c.Request.ContentLength != 0 && c.ShouldBindJSON(&input) != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查处理说明")
			return
		}
		if err := service.Invalidate(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP()); err != nil {
			writeAssetCandidateError(c, logger, "invalidate asset candidate", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/:id/mark-duplicate", func(c *gin.Context) {
		var input assetcandidate.DuplicateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请指定重复的正式资产")
			return
		}
		err := service.MarkDuplicate(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAssetCandidateError(c, logger, "mark asset candidate duplicate", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func writeAssetCandidateError(c *gin.Context, logger *slog.Logger, operation string, err error) {
	switch {
	case errors.Is(err, assetcandidate.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以管理待确认资产")
	case errors.Is(err, assetcandidate.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_asset_candidate", "请检查资产类型、资产值、所属组织和处理信息")
	case errors.Is(err, assetcandidate.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "待确认资产或关联正式资产不存在")
	case errors.Is(err, assetcandidate.ErrConflict):
		writeAPIError(c, http.StatusConflict, "invalid_state", "该待确认资产已处理，或正式资产存在重复")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理待确认资产失败")
	}
}
