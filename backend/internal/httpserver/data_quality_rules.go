package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"netassets/internal/dataquality"
)

func registerDataQualityRuleRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, svc *dataquality.Service, logger *slog.Logger) {
	g := api.Group("/data-quality-rules", requireAuth)
	g.GET("/scan", func(c *gin.Context) {
		result, err := svc.Scan(c.Request.Context(), mustPrincipal(c), dataquality.ScanFilter{
			OrganizationID: c.Query("organization_id"),
			ResourceType:   c.Query("resource_type"),
			Severity:       c.Query("severity"),
		}, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeDataQualityRuleError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
	g.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.Search(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			writeDataQualityRuleError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	g.POST("", func(c *gin.Context) {
		var input dataquality.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查数据质量规则")
			return
		}
		item, err := svc.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeDataQualityRuleError(c, logger, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	})
	g.PATCH("/:id", func(c *gin.Context) {
		var input dataquality.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查数据质量规则")
			return
		}
		item, err := svc.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		if err != nil {
			writeDataQualityRuleError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": item})
	})

	issues := api.Group("/data-quality-issues", requireAuth)
	issues.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, summary, err := svc.SearchIssues(c.Request.Context(), mustPrincipal(c), dataquality.IssueFilter{
			OrganizationID: c.Query("organization_id"), Status: c.Query("status"), Severity: c.Query("severity"),
			ResourceType: c.Query("resource_type"), Assignee: c.Query("assignee"), Page: page,
		})
		if err != nil {
			writeDataQualityIssueError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": result.Items, "summary": summary, "total": result.Total, "page": result.Page, "page_size": result.PageSize})
	})

	issues.GET("/:id/assignees", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.SearchIssueAssignees(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeDataQualityIssueError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	issues.GET("/:id/events", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.SearchIssueEvents(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeDataQualityIssueError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	issues.GET("/:id", func(c *gin.Context) {
		item, err := svc.GetIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeDataQualityIssueError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, item)
	})
	issues.POST("/:id/start", func(c *gin.Context) {
		var input dataquality.StartInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "整改信息格式不正确")
			return
		}
		item, err := svc.StartIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeDataQualityIssueResult(c, logger, item, err)
	})
	issues.POST("/:id/submit", func(c *gin.Context) {
		var input dataquality.SubmitInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "整改说明格式不正确")
			return
		}
		item, err := svc.SubmitIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeDataQualityIssueResult(c, logger, item, err)
	})
	issues.POST("/:id/review", func(c *gin.Context) {
		var input dataquality.ReviewInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "复核信息格式不正确")
			return
		}
		item, err := svc.ReviewIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeDataQualityIssueResult(c, logger, item, err)
	})
	issues.POST("/:id/exempt", func(c *gin.Context) {
		var input dataquality.ExemptInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "豁免信息格式不正确")
			return
		}
		item, err := svc.ExemptIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeDataQualityIssueResult(c, logger, item, err)
	})
	issues.POST("/:id/reopen", func(c *gin.Context) {
		var input struct {
			Note string `json:"note"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "重新打开说明格式不正确")
			return
		}
		item, err := svc.ReopenIssue(c.Request.Context(), mustPrincipal(c), c.Param("id"), input.Note, c.GetString("request_id"), c.ClientIP())
		writeDataQualityIssueResult(c, logger, item, err)
	})

	batches := api.Group("/data-quality-scan-batches", requireAuth)
	batches.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
			return
		}
		result, err := svc.SearchScanBatches(c.Request.Context(), mustPrincipal(c), page)
		if err != nil {
			writeDataQualityRuleError(c, logger, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})

}
func writeDataQualityRuleError(c *gin.Context, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, dataquality.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "仅集团管理员可以配置数据质量规则和执行数据质量检测")
	case errors.Is(err, dataquality.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "data_quality_rule_not_found", "数据质量规则不存在")
	case errors.Is(err, dataquality.ErrConflict):
		writeAPIError(c, http.StatusConflict, "data_quality_rule_exists", "同一对象和检查项的同类规则已存在")
	case errors.Is(err, dataquality.ErrInvalidTransition):
		writeAPIError(c, http.StatusConflict, "invalid_status_transition", "当前数据质量问题状态不允许执行此操作")
	case errors.Is(err, dataquality.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_data_quality_request", "请检查规则、筛选条件或问题处理内容")
	default:
		logger.Error("data quality rule", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理数据质量规则失败")
	}
}

func writeDataQualityIssueError(c *gin.Context, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, dataquality.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权处理该组织的数据质量问题")
	case errors.Is(err, dataquality.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "data_quality_issue_not_found", "数据质量问题不存在")
	case errors.Is(err, dataquality.ErrInvalidTransition):
		writeAPIError(c, http.StatusConflict, "invalid_status_transition", "当前数据质量问题状态不允许执行此操作")
	case errors.Is(err, dataquality.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_data_quality_issue_request", "请检查问题筛选条件、整改负责人或处理说明")
	default:
		logger.Error("data quality issue", "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理数据质量问题失败")
	}
}

func writeDataQualityIssueResult(c *gin.Context, logger *slog.Logger, item dataquality.Issue, err error) {
	if err != nil {
		writeDataQualityIssueError(c, logger, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": item})
}
