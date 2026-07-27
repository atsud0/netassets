package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"netassets/internal/backup"
)

func registerBackupRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *backup.Service, logger *slog.Logger) {
	routes := api.Group("/backups", requireAuth)
	routes.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), page)
		if handleBackupError(c, logger, err, "list backups") {
			return
		}
		c.JSON(http.StatusOK, result)
	})
	routes.POST("", func(c *gin.Context) {
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), c.GetString("request_id"), c.ClientIP())
		if handleBackupError(c, logger, err, "create backup") {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	routes.GET("/:id/verify", func(c *gin.Context) {
		item, err := service.Verify(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if handleBackupError(c, logger, err, "verify backup") {
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	routes.GET("/:id/files/:file", func(c *gin.Context) {
		path, contentType, err := service.FilePath(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("file"))
		if handleBackupError(c, logger, err, "download backup file") {
			return
		}
		c.Header("Content-Type", contentType)
		c.FileAttachment(path, filepath.Base(c.Param("id"))+"-"+filepath.Base(path))
	})
	routes.DELETE("/:id", func(c *gin.Context) {
		err := service.Delete(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		if handleBackupError(c, logger, err, "delete backup") {
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func handleBackupError(c *gin.Context, logger *slog.Logger, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, backup.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "backup_forbidden", "仅集团管理员可以管理系统备份")
	case errors.Is(err, backup.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "backup_not_found", "备份不存在或已删除")
	case errors.Is(err, backup.ErrBusy):
		writeAPIError(c, http.StatusConflict, "backup_in_progress", "已有备份任务正在执行，请等待完成后再试")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "backup_failed", "备份操作失败，请检查服务器日志后重试")
	}
	return true
}
