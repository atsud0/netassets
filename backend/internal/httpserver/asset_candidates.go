package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"netassets/internal/asset"
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
	group.GET("/:id/follow-ups", func(c *gin.Context) {
		items, err := service.ListFollowUps(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeAssetCandidateError(c, logger, "list asset candidate follow-ups", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	})
	group.POST("/:id/follow-ups", func(c *gin.Context) {
		var input assetcandidate.FollowUpInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请填写跟进类型和说明")
			return
		}
		item, err := service.AddFollowUp(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAssetCandidateError(c, logger, "add asset candidate follow-up", err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
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
	group.GET("/self-service-policies", func(c *gin.Context) {
		items, err := service.LoadSelfServicePolicies(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			writeAssetCandidateError(c, logger, "list asset candidate self service policies", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	})
	group.PUT("/self-service-policies", func(c *gin.Context) {
		var input assetcandidate.SelfServicePolicyInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查自助确认策略配置")
			return
		}
		item, err := service.SetSelfServicePolicy(c.Request.Context(), mustPrincipal(c), input)
		if err != nil {
			writeAssetCandidateError(c, logger, "save asset candidate self service policy", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("/batch-accept", func(c *gin.Context) {
		var input assetcandidate.BatchAcceptInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查批量确认请求内容")
			return
		}
		var afterCreate assetcandidate.BatchAcceptAfterCreate
		if findingService != nil {
			// Keep each candidate's working-version creation, candidate settlement
			// and imported-finding release in one transaction, just like the
			// single-item confirmation route.
			afterCreate = func(ctx context.Context, tx pgx.Tx, candidateID string, created asset.Asset) error {
				return findingService.LinkAcceptedAssetCandidateInTx(ctx, tx, mustPrincipal(c), candidateID, created.ID, c.GetString("request_id"), c.ClientIP())
			}
		}
		result := service.BatchAcceptWithAfterCreate(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP(), afterCreate)
		// A batch never fails as a whole: the response is 200 with per-row outcomes
		// so the reviewer sees which rows still need attention.
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
	group.POST("/:id/accept", func(c *gin.Context) {
		var input assetcandidate.AcceptInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请补充正式资产的入库信息")
			return
		}
		var afterCreate assetcandidate.AcceptAfterCreate
		if findingService != nil {
			// Keep the working-version insert, candidate settlement and imported
			// finding release in one transaction. A callback is used instead of a
			// package dependency so assetcandidate remains independent from the
			// securityworkorder domain.
			afterCreate = func(ctx context.Context, tx pgx.Tx, created asset.Asset) error {
				return findingService.LinkAcceptedAssetCandidateInTx(ctx, tx, mustPrincipal(c), c.Param("id"), created.ID, c.GetString("request_id"), c.ClientIP())
			}
		}
		item, err := service.AcceptWithAfterCreate(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP(), afterCreate)
		if err != nil {
			writeAssetCandidateError(c, logger, "accept asset candidate", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("/:id/retry-findings", func(c *gin.Context) {
		if findingService == nil {
			writeAPIError(c, http.StatusNotImplemented, "not_available", "漏洞关联服务不可用")
			return
		}
		result, err := findingService.RetryAcceptedAssetCandidate(c.Request.Context(), mustPrincipal(c), c.Param("id"), "", c.GetString("request_id"), c.ClientIP())
		if err != nil && result.Linked == 0 {
			writeFindingImportCandidateError(c, logger, "retry imported findings", err)
			return
		}
		response := gin.H{"result": result}
		if err != nil {
			response["warning"] = "部分漏洞关联失败，失败项仍可重试"
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
	case errors.Is(err, assetcandidate.ErrSelfServiceClosed):
		// Not a permission problem: the user may manage assets, the organization
		// simply has not been delegated the right to settle ownership.
		writeAPIError(c, http.StatusForbidden, "self_service_closed", "该组织未开通待确认资产自助确认，请联系集团管理员处理")
	case errors.Is(err, assetcandidate.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员或已开通自助确认的组织可以处理该待确认资产")
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
