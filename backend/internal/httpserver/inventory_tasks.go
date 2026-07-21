package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/inventorytask"
)

func registerInventoryTaskRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *inventorytask.Service, logger *slog.Logger) {
	group := api.Group("/inventory-tasks", requireAuth)
	group.GET("", func(c *gin.Context) {
		items, err := service.List(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			logger.Error("list inventory tasks", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取填报任务失败")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
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
	switch {
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
