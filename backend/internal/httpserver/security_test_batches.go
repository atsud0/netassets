package httpserver

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/securitytestbatch"
)

func registerSecurityTestBatchRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *securitytestbatch.Service, logger *slog.Logger) {
	group := api.Group("/security-test-batches", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), c.Query("status"), page)
		if err != nil {
			writeSecurityTestBatchError(c, logger, "list security test batches", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		writeSecurityTestBatchResult(c, logger, "get security test batch", http.StatusOK, item, err)
	})
	group.GET("/:id/events", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchEvents(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeSecurityTestBatchError(c, logger, "list security test batch events", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id/attachments", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchAttachments(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeSecurityTestBatchError(c, logger, "list security test batch attachments", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("/:id/attachments", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, securitytestbatch.MaxAttachmentSize+(1<<20))
		fileHeader, err := c.FormFile("file")
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_attachment", "请选择不超过 20MB 的附件")
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_attachment", "无法读取上传附件")
			return
		}
		defer file.Close()
		item, err := service.AddAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), securitytestbatch.AttachmentInput{
			Category: c.PostForm("category"), FileName: fileHeader.Filename, ContentType: fileHeader.Header.Get("Content-Type"),
			Description: c.PostForm("description"), SizeBytes: fileHeader.Size, Reader: file,
		}, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestBatchAttachmentResult(c, logger, "upload security test batch attachment", http.StatusCreated, item, err)
	})
	group.GET("/:id/attachments/:attachmentID/download", func(c *gin.Context) {
		item, err := service.GetAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"))
		if err != nil {
			writeSecurityTestBatchAttachmentResult(c, logger, "download security test batch attachment", http.StatusOK, securitytestbatch.Attachment{}, err)
			return
		}
		c.Header("Content-Type", item.ContentType)
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.FileName}))
		c.File(item.StoragePath)
	})
	group.DELETE("/:id/attachments/:attachmentID", func(c *gin.Context) {
		err := service.DeleteAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"), c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeSecurityTestBatchAttachmentResult(c, logger, "delete security test batch attachment", http.StatusNoContent, securitytestbatch.Attachment{}, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("", func(c *gin.Context) {
		var input securitytestbatch.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查安全测试批次信息")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestBatchResult(c, logger, "create security test batch", http.StatusCreated, item, err)
	})
	group.PATCH("/:id", func(c *gin.Context) {
		var input securitytestbatch.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查安全测试批次信息")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestBatchResult(c, logger, "update security test batch", http.StatusOK, item, err)
	})
	for path, handler := range map[string]func(c *gin.Context, input securitytestbatch.TransitionInput) (securitytestbatch.Batch, error){
		"authorize": func(c *gin.Context, input securitytestbatch.TransitionInput) (securitytestbatch.Batch, error) {
			return service.Authorize(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		},
		"start": func(c *gin.Context, input securitytestbatch.TransitionInput) (securitytestbatch.Batch, error) {
			return service.Start(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		},
		"complete": func(c *gin.Context, input securitytestbatch.TransitionInput) (securitytestbatch.Batch, error) {
			return service.Complete(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		},
		"cancel": func(c *gin.Context, input securitytestbatch.TransitionInput) (securitytestbatch.Batch, error) {
			return service.Cancel(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		},
	} {
		transitionPath := path
		transitionHandler := handler
		group.POST("/:id/"+transitionPath, func(c *gin.Context) {
			var input securitytestbatch.TransitionInput
			if c.Request.ContentLength != 0 {
				if err := c.ShouldBindJSON(&input); err != nil {
					writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查操作说明")
					return
				}
			}
			item, err := transitionHandler(c, input)
			writeSecurityTestBatchResult(c, logger, "transition security test batch: "+transitionPath, http.StatusOK, item, err)
		})
	}
}

func writeSecurityTestBatchResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securitytestbatch.Batch, err error) {
	if err != nil {
		writeSecurityTestBatchError(c, logger, operation, err)
		return
	}
	c.JSON(status, gin.H{"item": item})
}

func writeSecurityTestBatchError(c *gin.Context, logger *slog.Logger, operation string, err error) {
	switch {
	case errors.Is(err, securitytestbatch.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以管理安全测试批次")
	case errors.Is(err, securitytestbatch.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_security_test_batch", "请检查批次信息、测试时间和操作说明，范围需包含 1 至 200 项公网资产")
	case errors.Is(err, securitytestbatch.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "安全测试批次不存在")
	case errors.Is(err, securitytestbatch.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "安全测试批次当前状态不允许执行该操作，授权后范围不可修改")
	case errors.Is(err, securitytestbatch.ErrConflict):
		writeAPIError(c, http.StatusConflict, "scope_conflict", "测试范围包含不存在、已归档或非公网资产，请刷新资产后重试")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理安全测试批次失败")
	}
}

func writeSecurityTestBatchAttachmentResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securitytestbatch.Attachment, err error) {
	switch {
	case errors.Is(err, securitytestbatch.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权上传、下载或删除安全测试批次附件")
	case errors.Is(err, securitytestbatch.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_attachment", "仅支持 PDF、图片、TXT、CSV、XLSX、DOCX、ZIP，且单个附件不超过 20MB")
	case errors.Is(err, securitytestbatch.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "草稿或已取消的安全测试批次不允许上传附件")
	case errors.Is(err, securitytestbatch.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "安全测试批次或附件不存在")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理安全测试批次附件失败")
	default:
		c.JSON(status, gin.H{"item": item})
	}
}
