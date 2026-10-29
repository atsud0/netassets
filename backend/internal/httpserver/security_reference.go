package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/securityreference"
)

func registerSecurityReferenceRoutes(api *gin.RouterGroup, requireAuth gin.HandlerFunc, service *securityreference.Service, logger *slog.Logger) {
	group := api.Group("/security-reference", requireAuth)
	group.GET("/organizations", func(c *gin.Context) {
		filter, ok := securityReferenceFilter(c)
		if !ok {
			return
		}
		result, err := service.SearchOrganizations(c.Request.Context(), mustPrincipal(c), filter)
		writeSecurityReferenceResult(c, logger, "list security reference organizations", result, err)
	})
	group.GET("/assets", func(c *gin.Context) {
		filter, ok := securityReferenceFilter(c)
		if !ok {
			return
		}
		filter.PublicOnly = c.Query("public_only") == "true" || c.Query("public_only") == "1"
		result, err := service.SearchAssets(c.Request.Context(), mustPrincipal(c), filter)
		writeSecurityReferenceResult(c, logger, "list security reference assets", result, err)
	})
	group.POST("/assets/resolve", func(c *gin.Context) {
		var input securityreference.AssetSelectionInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "资产圈选信息格式不正确，圈选维度须为文本，所选标识须为文本数组")
			return
		}
		result, err := service.ResolvePublicAssets(c.Request.Context(), mustPrincipal(c), input)
		writeSecurityReferenceResult(c, logger, "resolve security reference assets", result, err)
	})
	group.GET("/business-systems", func(c *gin.Context) {
		filter, ok := securityReferenceFilter(c)
		if !ok {
			return
		}
		result, err := service.SearchBusinessSystems(c.Request.Context(), mustPrincipal(c), filter)
		writeSecurityReferenceResult(c, logger, "list security reference business systems", result, err)
	})
	group.GET("/network-areas", func(c *gin.Context) {
		filter, ok := securityReferenceFilter(c)
		if !ok {
			return
		}
		result, err := service.SearchNetworkAreas(c.Request.Context(), mustPrincipal(c), filter)
		writeSecurityReferenceResult(c, logger, "list security reference network areas", result, err)
	})
}

func securityReferenceFilter(c *gin.Context) (securityreference.Filter, bool) {
	page, err := parsePageQuery(c)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "invalid_query", pageQueryErrorMessage(err))
		return securityreference.Filter{}, false
	}
	return securityreference.Filter{
		Page: page, OrganizationID: strings.TrimSpace(c.Query("organization_id")), Keyword: strings.TrimSpace(c.Query("q")),
	}, true
}

func writeSecurityReferenceResult[T any](c *gin.Context, logger *slog.Logger, operation string, result T, err error) {
	var inputError *securityreference.InputValidationError
	switch {
	case errors.Is(err, securityreference.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无集团安全运营参考数据权限")
	case errors.Is(err, securityreference.ErrInvalid):
		message := "安全参考数据查询条件不正确"
		if errors.As(err, &inputError) {
			message = inputError.Message
		}
		writeAPIError(c, http.StatusBadRequest, "invalid_query", message)
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全运营参考数据失败")
	default:
		c.JSON(http.StatusOK, result)
	}
}
