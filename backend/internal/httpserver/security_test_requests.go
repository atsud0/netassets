package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"netassets/internal/securitytestrequest"
)

func registerSecurityTestRequestRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *securitytestrequest.Service, logger *slog.Logger) {
	group := api.Group("/security-test-requests", requireAuth)
	group.GET("", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.Search(c.Request.Context(), mustPrincipal(c), c.Query("status"), c.Query("keyword"), page)
		if err != nil {
			writeSecurityTestRequestError(c, logger, "search security test requests", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/scope-candidates", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchScopeCandidates(c.Request.Context(), mustPrincipal(c), securitytestrequest.ScopeCandidateFilter{
			Type: c.Query("asset_type"), Keyword: c.Query("keyword"), Page: page,
		})
		if err != nil {
			writeSecurityTestRequestError(c, logger, "search security test request scope candidates", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/contact-candidates", func(c *gin.Context) {
		items, err := service.SearchContactCandidates(c.Request.Context(), mustPrincipal(c))
		if err != nil {
			writeSecurityTestRequestError(c, logger, "search security test request contact candidates", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	})
	group.POST("", func(c *gin.Context) {
		var input securitytestrequest.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查上线安全测试申请信息")
			return
		}
		item, err := service.Create(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "create security test request", http.StatusCreated, item, err)
	})
	group.GET("/:id", func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		writeSecurityTestRequestResult(c, logger, "get security test request", http.StatusOK, item, err)
	})
	group.PATCH("/:id", func(c *gin.Context) {
		var input securitytestrequest.Input
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查上线安全测试申请信息")
			return
		}
		item, err := service.Update(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "update security test request", http.StatusOK, item, err)
	})
	group.GET("/:id/events", func(c *gin.Context) {
		page, err := parsePageQuery(c)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
			return
		}
		result, err := service.SearchEvents(c.Request.Context(), mustPrincipal(c), c.Param("id"), page)
		if err != nil {
			writeSecurityTestRequestError(c, logger, "list security test request events", err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id/quality-check", func(c *gin.Context) {
		result, err := service.QualityCheck(c.Request.Context(), mustPrincipal(c), c.Param("id"))
		if err != nil {
			writeSecurityTestRequestError(c, logger, "check security test request scope quality", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"item": result})
	})
	group.POST("/:id/submit", func(c *gin.Context) {
		item, err := service.Submit(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "submit security test request", http.StatusOK, item, err)
	})
	group.POST("/:id/withdraw", func(c *gin.Context) {
		item, err := service.Withdraw(c.Request.Context(), mustPrincipal(c), c.Param("id"), c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "withdraw security test request", http.StatusOK, item, err)
	})
	group.POST("/:id/approve", func(c *gin.Context) {
		var input securitytestrequest.ApproveInput
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查授权说明和测试窗口")
				return
			}
		}
		item, err := service.Approve(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "approve security test request", http.StatusOK, item, err)
	})
	group.POST("/:id/reject", func(c *gin.Context) {
		var input securitytestrequest.TransitionInput
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "请填写驳回原因")
				return
			}
		}
		item, err := service.Reject(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "reject security test request", http.StatusOK, item, err)
	})
	group.POST("/:id/cancel", func(c *gin.Context) {
		var input securitytestrequest.TransitionInput
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "请填写取消原因")
				return
			}
		}
		item, err := service.Cancel(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "cancel security test request", http.StatusOK, item, err)
	})
	group.POST("/:id/approve-launch", func(c *gin.Context) {
		var input securitytestrequest.ApproveLaunchInput
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查上线批准说明")
				return
			}
		}
		item, err := service.ApproveLaunch(c.Request.Context(), mustPrincipal(c), c.Param("id"), input, c.GetString("request_id"), c.ClientIP())
		writeSecurityTestRequestResult(c, logger, "approve security test request launch", http.StatusOK, item, err)
	})
}

func writeSecurityTestRequestResult(c *gin.Context, logger *slog.Logger, operation string, status int, item securitytestrequest.Request, err error) {
	if err != nil {
		writeSecurityTestRequestError(c, logger, operation, err)
		return
	}
	c.JSON(status, gin.H{"item": item})
}

func writeSecurityTestRequestError(c *gin.Context, logger *slog.Logger, operation string, err error) {
	var qualityError *securitytestrequest.ScopeQualityError
	var findingsError *securitytestrequest.OpenFindingsError
	var unbindableError *securitytestrequest.UnbindableScopeError
	switch {
	case errors.As(err, &qualityError):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":         gin.H{"code": "quality_check_failed", "message": "申请范围内存在必填项缺失，请补全后重新提交"},
			"quality_check": qualityError.Check,
		})
	case errors.As(err, &findingsError):
		c.JSON(http.StatusConflict, gin.H{
			"error": gin.H{"code": "findings_not_closed", "message": "申请范围仍有未闭环的安全发现，请先完成整改或风险接受"},
			"items": findingsError.Items,
		})
	case errors.As(err, &unbindableError):
		writeAPIError(c, http.StatusConflict, "scope_unbindable", "申请范围内有资源已进入其他填报流程或被废弃，无法自动上报；请联系申请组织处理")
	case errors.Is(err, securitytestrequest.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无权执行该操作")
	case errors.Is(err, securitytestrequest.ErrInvalid):
		message := "请检查申请信息：范围需包含 1 至 200 项公网资产，联系人和上线对象必须填写"
		var validationError *securitytestrequest.InputValidationError
		if errors.As(err, &validationError) {
			message = validationError.Message
		}
		writeAPIError(c, http.StatusBadRequest, "invalid_request", message)
	case errors.Is(err, securitytestrequest.ErrNotFound):
		writeAPIError(c, http.StatusNotFound, "not_found", "上线安全测试申请不存在")
	case errors.Is(err, securitytestrequest.ErrState):
		writeAPIError(c, http.StatusConflict, "invalid_state", "申请当前状态不允许执行该操作")
	case errors.Is(err, securitytestrequest.ErrConflict):
		writeAPIError(c, http.StatusConflict, "scope_conflict", "申请范围包含不可用资源（已删除、非公网或已进入其他流程），请刷新后重试")
	default:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "处理上线安全测试申请失败")
	}
}
