package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/asset"
	"netassets/internal/exportfile"
	"netassets/internal/inventorytask"
)

func registerInventoryTaskRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *inventorytask.Service, logger *slog.Logger) {
	group := api.Group("/inventory-tasks", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), strings.TrimSpace(c.Query("task_id")), page)
		if err != nil {
			logger.Error("list inventory tasks", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报任务失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:taskID/organizations/:organizationID/business-system-drafts", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchBusinessSystemDrafts(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), page)
		writeInventoryBusinessSystemDraftResult(c, logger, "list inventory business system drafts", result, err)
	})
	group.POST("/:taskID/organizations/:organizationID/business-systems/:systemID/update-draft", func(c *gin.Context) {
		var input inventorytask.BusinessSystemDraftInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统草稿信息格式不正确")
			return
		}
		item, err := service.CreateBusinessSystemUpdateDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("systemID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "create business system update draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/business-systems/:systemID/delete-draft", func(c *gin.Context) {
		item, err := service.CreateBusinessSystemDeleteDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("systemID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "create business system delete draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/business-system-drafts/batch-select", func(c *gin.Context) {
		var input inventorytask.WorkspaceSelectionBatchInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择需要上报的业务系统，单次最多 200 项")
			return
		}
		result, err := service.SelectWorkspaceBusinessSystemsBatch(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "batch select workspace business systems", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
	group.POST("/:taskID/organizations/:organizationID/business-system-drafts/:draftID/select", func(c *gin.Context) {
		item, err := service.SelectWorkspaceBusinessSystem(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "select workspace business system", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/business-system-drafts", func(c *gin.Context) {
		var input inventorytask.BusinessSystemDraftInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统草稿信息格式不正确")
			return
		}
		item, err := service.CreateBusinessSystemDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "create inventory business system draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.PATCH("/:taskID/organizations/:organizationID/business-system-drafts/:draftID", func(c *gin.Context) {
		var input inventorytask.BusinessSystemDraftInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "业务系统草稿信息格式不正确")
			return
		}
		item, err := service.UpdateBusinessSystemDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "update inventory business system draft", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.DELETE("/:taskID/organizations/:organizationID/business-system-drafts/:draftID", func(c *gin.Context) {
		err := service.DeleteBusinessSystemDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryBusinessSystemDraftError(c, logger, "delete inventory business system draft", err) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.GET("/:taskID/organizations/:organizationID/asset-drafts", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchAssetDrafts(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), page)
		writeInventoryAssetDraftResult(c, logger, "list inventory asset drafts", result, err)
	})
	group.POST("/:taskID/organizations/:organizationID/assets/:assetID/update-draft", func(c *gin.Context) {
		var input asset.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "资产草稿信息格式不正确")
			return
		}
		item, err := service.CreateAssetUpdateDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("assetID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "create asset update draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/assets/:assetID/delete-draft", func(c *gin.Context) {
		item, err := service.CreateAssetDeleteDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("assetID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "create asset delete draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/asset-drafts/batch-select", func(c *gin.Context) {
		var input inventorytask.WorkspaceSelectionBatchInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择需要上报的网络资产，单次最多 200 项")
			return
		}
		result, err := service.SelectWorkspaceAssetsBatch(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "batch select workspace assets", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
	group.POST("/:taskID/organizations/:organizationID/asset-drafts/:draftID/select", func(c *gin.Context) {
		item, err := service.SelectWorkspaceAsset(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "select workspace asset", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.POST("/:taskID/organizations/:organizationID/asset-drafts", func(c *gin.Context) {
		var input asset.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "资产草稿信息格式不正确")
			return
		}
		item, err := service.CreateAssetDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "create inventory asset draft", err) {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	group.PATCH("/:taskID/organizations/:organizationID/asset-drafts/:draftID", func(c *gin.Context) {
		var input asset.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "资产草稿信息格式不正确")
			return
		}
		item, err := service.UpdateAssetDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), input, c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "update inventory asset draft", err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	group.DELETE("/:taskID/organizations/:organizationID/asset-drafts/:draftID", func(c *gin.Context) {
		err := service.DeleteAssetDraft(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Param("draftID"), c.GetString("request_id"), c.ClientIP())
		if writeInventoryAssetDraftError(c, logger, "delete inventory asset draft", err) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.GET("/:taskID/organizations", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchOrganizations(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), strings.TrimSpace(c.Query("organization_id")), page)
		switch {
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_inventory_task_id", "填报任务编号不正确")
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "inventory_task_not_found", "填报任务不存在或当前账号无权查看")
		case err != nil:
			logger.Error("list inventory task organizations", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报组织进度失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.GET("/:taskID/export", func(c *gin.Context) {
		format := c.DefaultQuery("format", "xlsx")
		extendExportWriteDeadline(c, logger)
		if isCSVExport(format) {
			_, err := service.StreamCSVExport(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Writer, func(meta exportfile.Metadata) {
				setStreamingExportHeaders(c, "inventory-task-results.csv", meta)
			}, c.GetString("request_id"), c.ClientIP())
			if err == nil {
				return
			}
			if c.Writer.Written() {
				logger.Error("stream inventory task csv", "error", err, "written_bytes", c.Writer.Size(), "request_id", c.GetString("request_id"))
				c.Abort()
				return
			}
			clearStreamingExportHeaders(c)
			switch {
			case errors.Is(err, inventorytask.ErrNotFound):
				writeAPIError(c, http.StatusNotFound, "inventory_task_not_found", "填报任务不存在或当前账号无权导出")
			case errors.Is(err, inventorytask.ErrForbidden):
				writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权导出填报任务")
			default:
				logger.Error("export inventory task csv", "error", err, "request_id", c.GetString("request_id"))
				writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出填报任务结果失败")
			}
			return
		}

		data, contentType, fileName, count, err := service.Export(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), format, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, inventorytask.ErrExportTooLarge):
			writeAPIError(c, http.StatusUnprocessableEntity, "synchronous_xlsx_limit_exceeded", "同步 XLSX 导出最多支持 5000 条数据，请改用 CSV 格式导出")
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "导出格式仅支持 csv 或 xlsx")
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "inventory_task_not_found", "填报任务不存在或当前账号无权导出")
		case err != nil:
			logger.Error("export inventory task", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "导出填报任务结果失败")
		default:
			extension := strings.TrimPrefix(strings.ToLower(format), ".")
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=inventory-task-results.%s; filename*=UTF-8''%s", extension, url.PathEscape(fileName)))
			c.Header("X-Export-Count", fmt.Sprintf("%d", count))
			c.Data(http.StatusOK, contentType, data)
		}
	})

	group.POST("/scope-preview", func(c *gin.Context) {
		var input inventorytask.ScopePreviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "任务范围信息格式不正确")
			return
		}
		result, err := service.PreviewScope(c.Request.Context(), mustPrincipal(c), input)
		switch {
		case errors.Is(err, inventorytask.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以预览填报任务范围")
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_inventory_task_scope", "请检查填报组织、填报内容和网络资产核查范围")
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "部分填报组织不存在、已停用或不是下级组织")
		case err != nil:
			logger.Error("preview inventory task scope", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "预览填报任务范围失败")
		default:
			c.JSON(http.StatusOK, gin.H{"scope_preview": result})
		}
	})
	group.POST("", func(c *gin.Context) {
		var input inventorytask.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "填报任务信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, inventorytask.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以创建填报任务")
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_inventory_task", "请检查任务名称、时间范围和填报组织")
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "organization_not_found", "部分填报组织不存在、已停用或不是下级组织")
		case err != nil:
			logger.Error("create inventory task", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "创建填报任务失败")
		default:
			c.JSON(http.StatusCreated, gin.H{"item": item})
		}
	})
	group.GET("/:taskID/organizations/:organizationID/submission-diff", func(c *gin.Context) {
		result, err := service.SubmissionDiff(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"))
		switch {
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "submission_not_found", "尚未生成填报提交快照，或当前账号无权查看")
		case err != nil:
			logger.Error("load inventory submission diff", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报提交差异失败")
		default:
			c.JSON(http.StatusOK, gin.H{"submission_diff": result})
		}
	})
	group.GET("/:taskID/organizations/:organizationID/submission-diff/items", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchSubmissionDiffItems(
			c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"),
			c.Query("resource_type"), c.Query("change_type"), page,
		)
		switch {
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "resource_type 或 change_type 参数不正确")
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "submission_not_found", "尚未生成填报提交快照，或当前账号无权查看")
		case err != nil:
			logger.Error("list inventory submission diff items", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报提交差异明细失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.GET("/:taskID/organizations/:organizationID/review-context", func(c *gin.Context) {
		result, err := service.ReviewContext(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"))
		switch {
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "submission_not_found", "尚未生成填报提交快照，或当前账号无权查看")
		case err != nil:
			logger.Error("load inventory review context", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取逐条审核意见失败")
		default:
			c.JSON(http.StatusOK, gin.H{"review_context": result})
		}
	})
	group.GET("/:taskID/organizations/:organizationID/review-resources", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchReviewResources(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.Query("q"), page)
		switch {
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "submission_not_found", "尚未生成填报提交快照，或当前账号无权查看")
		case err != nil:
			logger.Error("list inventory review resources", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取本次提交明细失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.GET("/:taskID/organizations/:organizationID/review-comments", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchReviewComments(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), page)
		switch {
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "submission_not_found", "尚未生成填报提交快照，或当前账号无权查看")
		case err != nil:
			logger.Error("list inventory review comments", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取逐条审核意见失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	group.GET("/:taskID/organizations/:organizationID/quality-check", func(c *gin.Context) {
		result, err := service.QualityCheck(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"))
		switch {
		case errors.Is(err, inventorytask.ErrNotFound):
			writeAPIError(c, http.StatusNotFound, "not_found", "填报任务或目标组织不存在")
		case err != nil:
			logger.Error("check inventory task quality", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "执行填报质量检查失败")
		default:
			c.JSON(http.StatusOK, gin.H{"quality_check": result})
		}
	})
	group.POST("/:taskID/organizations/:organizationID/start", func(c *gin.Context) {
		item, err := service.Start(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), c.GetString("request_id"), c.ClientIP())
		writeInventoryProgressResult(c, logger, "start inventory task", "开始填报任务失败", item, err)
	})
	group.POST("/:taskID/organizations/:organizationID/submit", func(c *gin.Context) {
		var input inventorytask.SubmissionInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "提交信息格式不正确")
			return
		}
		item, err := service.Submit(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		writeInventoryProgressResult(c, logger, "submit inventory task", "提交填报任务失败", item, err)
	})
	group.POST("/:taskID/organizations/:organizationID/review", func(c *gin.Context) {
		var input inventorytask.ReviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "审核信息格式不正确")
			return
		}
		item, err := service.Review(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), c.Param("organizationID"), input, c.GetString("request_id"), c.ClientIP())
		writeInventoryProgressResult(c, logger, "review inventory task", "审核填报任务失败", item, err)
	})
	group.PATCH("/:taskID/status", func(c *gin.Context) {
		var input inventorytask.StatusInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "任务状态格式不正确")
			return
		}
		err := service.UpdateStatus(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, inventorytask.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以更新任务状态")
		case errors.Is(err, inventorytask.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_status", "不支持该任务状态")
		case errors.Is(err, inventorytask.ErrState):
			writeAPIError(c, http.StatusConflict, "invalid_state", "任务当前状态不允许执行该操作")
		case err != nil:
			logger.Error("update inventory task status", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "更新填报任务状态失败")
		default:
			c.JSON(http.StatusOK, gin.H{"status": input.Status})
		}
	})
}

func writeInventoryProgressResult(c *gin.Context, logger *slog.Logger, operation, fallback string, item inventorytask.OrganizationProgress, err error) {
	var qualityError *inventorytask.QualityCheckError
	switch {
	case errors.As(err, &qualityError):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":         gin.H{"code": "quality_check_failed", "message": "数据质量检查未通过，请修正错误项后重新提交"},
			"quality_check": qualityError.Result,
		})
	case errors.Is(err, inventorytask.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "您没有权限执行该操作")
	case errors.Is(err, inventorytask.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_input", "请检查提交或审核信息")
	case errors.Is(err, inventorytask.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "填报任务或目标组织不存在")
	case errors.Is(err, inventorytask.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "任务未下发、未到开始时间、已截止，或当前状态不允许执行该操作")
	case errors.Is(err, inventorytask.ErrResourceInUse):
		writeAPIError(c, http.StatusConflict, "resource_in_use", "业务系统仍关联有效网络资产，无法发布删除变更；请先解除关联或取消删除变更")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", fallback)
	default:
		c.JSON(http.StatusOK, gin.H{"item": item})
	}
}

func writeInventoryBusinessSystemDraftResult(c *gin.Context, logger *slog.Logger, operation string, result any, err error) {
	if writeInventoryBusinessSystemDraftError(c, logger, operation, err) {
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeInventoryBusinessSystemDraftError(c *gin.Context, logger *slog.Logger, operation string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, inventorytask.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权维护该组织的填报业务系统草稿")
	case errors.Is(err, inventorytask.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_business_system_draft", "请检查业务系统字段和集团自定义字段")
	case errors.Is(err, inventorytask.ErrConflict):
		writeAPIError(c, http.StatusConflict, "business_system_draft_conflict", "相同系统编码已存在于正式台账或当前任务草稿中")
	case errors.Is(err, inventorytask.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "business_system_draft_not_found", "填报任务、组织或业务系统草稿不存在")
	case errors.Is(err, inventorytask.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "任务当前状态不允许修改业务系统草稿，请先开始填报或等待退回")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理填报业务系统草稿失败")
	}
	return true
}

func writeInventoryAssetDraftResult(c *gin.Context, logger *slog.Logger, operation string, result any, err error) {
	if writeInventoryAssetDraftError(c, logger, operation, err) {
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeInventoryAssetDraftError(c *gin.Context, logger *slog.Logger, operation string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, inventorytask.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权维护该组织的填报资产草稿")
	case errors.Is(err, inventorytask.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_asset_draft", "请检查资产字段、所属资源或任务网络范围")
	case errors.Is(err, inventorytask.ErrConflict):
		writeAPIError(c, http.StatusConflict, "asset_draft_conflict", "相同资产已存在于正式台账或当前任务草稿中")
	case errors.Is(err, inventorytask.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "asset_draft_not_found", "填报任务、组织或资产草稿不存在")
	case errors.Is(err, inventorytask.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "任务当前状态不允许修改资产草稿，请先开始填报或等待退回")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理填报资产草稿失败")
	}
	return true
}
