package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"netassets/internal/exportjob"
)

func registerExportJobRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *exportjob.Service, logger *slog.Logger) {
	routes := api.Group("/export-jobs", requireAuth)
	routes.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page, c.Query("status"), c.Query("resource_type"))
		if handleExportJobError(c, logger, err, "list export jobs") {
			return
		}
		c.JSON(http.StatusOK, result)
	})
	routes.POST("", func(c *gin.Context) {
		var input exportjob.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "后台导出任务信息格式不正确")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		if handleExportJobError(c, logger, err, "create export job") {
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"item": item})
	})
	routes.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if handleExportJobError(c, logger, err, "get export job") {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	routes.GET("/:id/download", func(c *gin.Context) {
		extendExportWriteDeadline(c, logger)
		path, item, err := service.Download(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if handleExportJobError(c, logger, err, "download export job") {
			return
		}
		c.Header("Content-Type", item.ContentType)
		c.Header("Content-Disposition", "attachment; filename=export.xlsx; filename*=UTF-8''"+url.PathEscape(item.FileName))
		c.File(path)
	})
	routes.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if handleExportJobError(c, logger, err, "delete export job") {
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func handleExportJobError(c *gin.Context, logger *slog.Logger, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, exportjob.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "export_job_forbidden", "当前账号无权导出所选资源")
	case errors.Is(err, exportjob.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_export_job", "请检查导出资源类型、格式和筛选条件")
	case errors.Is(err, exportjob.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "export_job_not_found", "导出任务不存在、已删除或不属于当前账号")
	case errors.Is(err, exportjob.ErrBusy):
		writeAPIError(c, http.StatusConflict, "too_many_export_jobs", "当前已有 3 个待处理导出任务，请等待完成后再创建")
	case errors.Is(err, exportjob.ErrExpired):
		writeAPIError(c, http.StatusGone, "export_job_expired", "导出文件已过期并清理，请重新创建导出任务")
	case errors.Is(err, exportjob.ErrConflict):
		writeAPIError(c, http.StatusConflict, "export_job_not_ready", "导出任务尚未完成或正在运行，暂不能执行此操作")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "export_job_failed", "后台导出任务操作失败，请稍后重试")
	}
	return true
}
