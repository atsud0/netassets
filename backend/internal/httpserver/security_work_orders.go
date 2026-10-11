package httpserver

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/securityworkorder"
)

func registerSecurityWorkOrderRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *securityworkorder.Service, logger *slog.Logger) {
	findings := api.Group("/security-findings", requireAuth)
	findings.GET("/import-template", func(c *gin.Context) {
		data, contentType, fileName, err := securityworkorder.FindingImportTemplate(c.DefaultQuery("format", "xlsx"))
		if errors.Is(err, securityworkorder.ErrInvalid) {
			writeAPIError(c, http.StatusBadRequest, "invalid_format", "模板格式仅支持 CSV 或 XLSX")
			return
		}
		if err != nil {
			logger.Error("generate finding import template", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "生成漏洞导入模板失败")
			return
		}
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
		c.Data(http.StatusOK, contentType, data)
	})
	findings.GET("/imports", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchFindingImports(c.Request.Context(), mustPrincipal(c), page)
		switch {
		case errors.Is(err, securityworkorder.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无安全发现导入历史查看权限")
		case err != nil:
			logger.Error("list finding imports", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取漏洞导入历史失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	findings.POST("/import", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11<<20)
		fileHeader, err := c.FormFile("file")
		if err != nil || fileHeader.Size <= 0 || fileHeader.Size > 10<<20 {
			writeAPIError(c, http.StatusBadRequest, "invalid_import_file", "请选择不超过 10MB 的 CSV、XLSX 或 JSON 文件")
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_import_file", "无法读取上传文件")
			return
		}
		defer file.Close()
		result, err := service.ImportFindings(c.Request.Context(), mustPrincipal(c), c.PostForm("test_batch_id"), fileHeader.Filename, file, c.GetString("request_id"), c.ClientIP())
		writeFindingImportResult(c, logger, result, err)
	})

	findings.GET("/import-candidates", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchFindingImportCandidates(c.Request.Context(), mustPrincipal(c), securityworkorder.FindingImportCandidateFilter{
			TestBatchID: c.Query("test_batch_id"), Status: c.Query("status"), AssetType: c.Query("asset_type"),
			Keyword: c.Query("keyword"), Page: page,
		})
		if err != nil {
			writeFindingImportCandidateError(c, logger, "list finding import candidates", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	findings.GET("/import-candidates/:id/scope-assets", func(c *gin.Context) {
		items, err := service.ListFindingImportScopeOptions(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeFindingImportCandidateError(c, logger, "list finding import candidate scope assets", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items), "page": 1, "page_size": 200})
	})
	findings.POST("/import-candidates/:id/link", func(c *gin.Context) {
		var input securityworkorder.FindingImportCandidateLinkInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请选择要关联的资产")
			return
		}
		item, err := service.LinkFindingImportCandidate(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeFindingImportCandidateError(c, logger, "link finding import candidate", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})
	findings.POST("/import-candidates/:id/ignore", func(c *gin.Context) {
		var input securityworkorder.FindingImportCandidateReviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请填写忽略原因")
			return
		}
		item, err := service.IgnoreFindingImportCandidate(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeFindingImportCandidateError(c, logger, "ignore finding import candidate", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})

	findings.GET("/summary", func(c *gin.Context) {
		result, err := service.SummarizeFindings(c.Request.Context(), mustPrincipal(c), c.Query("organization_id"))
		switch {
		case errors.Is(err, securityworkorder.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该组织的安全发现汇总")
		case errors.Is(err, securityworkorder.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "安全发现组织筛选条件不正确")
		case err != nil:
			logger.Error("summarize security findings", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全发现汇总失败")
		default:
			c.JSON(http.StatusOK, gin.H{"summary": result})
		}
	})

	findings.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchFindings(c.Request.Context(), mustPrincipal(c), c.Query("organization_id"), c.Query("status"), c.Query("asset_link_state"), page)
		switch {
		case errors.Is(err, securityworkorder.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该组织的安全发现")
		case errors.Is(err, securityworkorder.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "安全发现筛选条件不正确")
		case err != nil:
			logger.Error("list security findings", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全发现失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	findings.GET("/:id", func(c *gin.Context) {
		item, err := service.GetFinding(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		writeFindingResult(c, logger, "get security finding", http.StatusOK, item, err)
	})
	findings.POST("/:id/accept-risk", func(c *gin.Context) {
		var input securityworkorder.RiskAcceptanceInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "风险接受信息格式不正确")
			return
		}
		item, err := service.AcceptRisk(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeFindingRiskResult(c, logger, "accept security finding risk", item, err)
	})
	findings.POST("/:id/revoke-risk", func(c *gin.Context) {
		item, err := service.RevokeRisk(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		writeFindingRiskResult(c, logger, "revoke security finding risk", item, err)
	})
	findings.POST("/:id/review-risk", func(c *gin.Context) {
		var input securityworkorder.RiskReviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "风险复核信息格式不正确")
			return
		}
		item, err := service.ReviewRisk(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeFindingRiskResult(c, logger, "review security finding risk", item, err)
	})

	findings.POST("", func(c *gin.Context) {
		var input securityworkorder.FindingInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "安全发现信息格式不正确")
			return
		}
		item, err := service.CreateFinding(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeFindingResult(c, logger, "create security finding", http.StatusCreated, item, err)
	})
	findings.PATCH("/:id", func(c *gin.Context) {
		var input securityworkorder.FindingInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "安全发现信息格式不正确")
			return
		}
		item, err := service.UpdateFinding(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeFindingResult(c, logger, "update security finding", http.StatusOK, item, err)
	})

	orders := api.Group("/security-work-orders", requireAuth)
	orders.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		overdue, valid := parseOptionalBoolQuery(c.Query("overdue"))
		if !valid {
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "仅逾期筛选条件不正确")
			return
		}
		result, err := service.SearchWorkOrdersWithFilter(c.Request.Context(), mustPrincipal(c), securityworkorder.WorkOrderSearchFilter{
			OrganizationID: c.Query("organization_id"),
			Status:         c.Query("status"),
			OverdueOnly:    overdue,
			Page:           page,
		})
		switch {
		case errors.Is(err, securityworkorder.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该组织的安全工单")
		case errors.Is(err, securityworkorder.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_filter", "安全工单筛选条件不正确")
		case err != nil:
			logger.Error("list security work orders", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全工单失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
	orders.GET("/:id", func(c *gin.Context) {
		item, err := service.GetWorkOrder(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		writeWorkOrderResult(c, logger, "get security work order", http.StatusOK, item, err)
	})
	orders.GET("/:id/findings", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchWorkOrderFindings(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeWorkOrderChildError(c, logger, "list security work order findings", "读取工单关联安全发现失败", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	orders.GET("/:id/events", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchWorkOrderEvents(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeWorkOrderChildError(c, logger, "list security work order events", "读取工单处理记录失败", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	orders.GET("/:id/attachments", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := service.SearchWorkOrderAttachments(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeWorkOrderChildError(c, logger, "list security work order attachments", "读取工单附件失败", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	orders.POST("", func(c *gin.Context) {
		var input securityworkorder.WorkOrderInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "安全工单信息格式不正确")
			return
		}
		item, err := service.CreateWorkOrder(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "create security work order", http.StatusCreated, item, err)
	})
	orders.POST("/:id/issue", func(c *gin.Context) {
		var input securityworkorder.CommentInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "下发信息格式不正确")
			return
		}
		item, err := service.Issue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "issue security work order", http.StatusOK, item, err)
	})
	orders.POST("/:id/accept", func(c *gin.Context) {
		var input securityworkorder.CommentInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "接收信息格式不正确")
			return
		}
		item, err := service.Accept(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "accept security work order", http.StatusOK, item, err)
	})
	orders.POST("/:id/progress", func(c *gin.Context) {
		var input securityworkorder.ProgressInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "整改进度格式不正确")
			return
		}
		item, err := service.Progress(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "update security work order progress", http.StatusOK, item, err)
	})
	orders.POST("/:id/submit", func(c *gin.Context) {
		var input securityworkorder.SubmitInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "整改结果格式不正确")
			return
		}
		item, err := service.Submit(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "submit security work order", http.StatusOK, item, err)
	})
	orders.POST("/:id/review", func(c *gin.Context) {
		var input securityworkorder.ReviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "复测信息格式不正确")
			return
		}
		item, err := service.Review(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "review security work order", http.StatusOK, item, err)
	})
	orders.POST("/:id/cancel", func(c *gin.Context) {
		var input securityworkorder.CommentInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "取消信息格式不正确")
			return
		}
		item, err := service.Cancel(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeWorkOrderResult(c, logger, "cancel security work order", http.StatusOK, item, err)
	})
	orders.POST("/:id/attachments", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, securityworkorder.MaxAttachmentSize+(1<<20))
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
		item, err := service.AddAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), securityworkorder.AttachmentInput{
			Category: c.PostForm("category"), FileName: fileHeader.Filename, ContentType: fileHeader.Header.Get("Content-Type"),
			Description: c.PostForm("description"), SizeBytes: fileHeader.Size, Reader: file,
		}, c.GetString("request_id"), c.ClientIP())
		writeAttachmentResult(c, logger, "upload security work order attachment", http.StatusCreated, item, err)
	})
	orders.GET("/:id/attachments/:attachmentID/download", func(c *gin.Context) {
		item, err := service.GetAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"))
		if err != nil {
			writeAttachmentResult(c, logger, "download security work order attachment", http.StatusOK, securityworkorder.Attachment{}, err)
			return
		}
		c.Header("Content-Type", item.ContentType)
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.FileName}))
		c.File(item.StoragePath)
	})
	orders.DELETE("/:id/attachments/:attachmentID", func(c *gin.Context) {
		err := service.DeleteAttachment(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.Param("attachmentID"), c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeAttachmentResult(c, logger, "delete security work order attachment", http.StatusNoContent, securityworkorder.Attachment{}, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func writeFindingResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securityworkorder.Finding, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看、登记或修改该安全发现")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_finding", "请检查责任组织、关联资产、严重等级和漏洞信息")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "reference_not_found", "安全发现、责任组织、关联资产或业务系统不存在或不匹配")
	case errors.Is(err, securityworkorder.ErrConflict):
		writeAPIError(c, http.StatusConflict, "finding_conflict", "安全发现当前不能执行该操作")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "保存安全发现失败")
	default:
		c.JSON(status, gin.H{"item": item})
	}
}

func writeFindingRiskResult(c *gin.Context, logger *slog.Logger, operation string, item securityworkorder.Finding, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无安全发现风险管理权限")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_risk_acceptance", "请填写有效的风险接受原因、未来有效期或复核意见")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "finding_not_found", "安全发现不存在或当前账号不可见")
	case errors.Is(err, securityworkorder.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_finding_state", "安全发现当前状态不允许执行风险操作")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理安全发现风险状态失败")
	default:
		c.JSON(http.StatusOK, gin.H{"item": item})
	}
}

func writeFindingImportCandidateError(c *gin.Context, logger *slog.Logger, operation string, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无漏洞导入待关联项处理权限")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_candidate", "待关联项、筛选条件、授权资产或处理备注不正确")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "candidate_not_found", "待关联漏洞或测试批次授权资产不存在")
	case errors.Is(err, securityworkorder.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_candidate_state", "该待关联漏洞已处理，请刷新列表")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理漏洞导入待关联项失败")
	}
}

func writeFindingImportResult(c *gin.Context, logger *slog.Logger, result securityworkorder.FindingImportResult, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无安全发现导入权限")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_finding_import", "请检查测试批次、文件格式、表头和数据行；最多支持 5000 行")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "test_batch_not_found", "安全测试批次不存在")
	case errors.Is(err, securityworkorder.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_batch_state", "仅已授权、测试中或已完成的安全测试批次可以导入漏洞")
	case err != nil:
		logger.Error("import security findings", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量导入安全发现失败")
	default:
		c.JSON(http.StatusCreated, gin.H{"item": result})
	}
}

func writeWorkOrderChildError(c *gin.Context, logger *slog.Logger, operation, internalMessage string, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权查看该安全工单")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_work_order_id", "安全工单编号不正确")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "work_order_not_found", "安全工单不存在")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", internalMessage)
	}
}

func writeWorkOrderResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securityworkorder.WorkOrder, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权执行该安全工单操作")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_work_order", "请检查责任组织、关联漏洞、整改期限和操作内容")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "安全工单、责任组织或关联漏洞不存在或不匹配")
	case errors.Is(err, securityworkorder.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "安全工单当前状态不允许执行该操作")
	case errors.Is(err, securityworkorder.ErrConflict):
		writeAPIError(c, http.StatusConflict, "work_order_conflict", "安全工单当前不能执行该操作")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理安全工单失败")
	default:
		c.JSON(status, gin.H{"item": item})
	}
}

func writeAttachmentResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securityworkorder.Attachment, err error) {
	switch {
	case errors.Is(err, securityworkorder.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权上传、下载或删除该工单附件")
	case errors.Is(err, securityworkorder.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_attachment", "仅支持 PDF、图片、TXT、CSV、XLSX、DOCX、ZIP，且单个附件不超过 20MB")
	case errors.Is(err, securityworkorder.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "安全工单或附件不存在")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理安全工单附件失败")
	default:
		c.JSON(status, gin.H{"item": item})
	}
}
