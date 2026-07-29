package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

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
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			logger.Error("list inventory tasks", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报任务失败")
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:taskID/organizations", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchOrganizations(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), page)
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
		data, contentType, fileName, count, err := service.Export(c.Request.Context(), mustPrincipal(c), c.Param("taskID"), format, c.GetString("request_id"), c.ClientIP())
		switch {
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
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", fallback)
	default:
		c.JSON(http.StatusOK, gin.H{"item": item})
	}
}
