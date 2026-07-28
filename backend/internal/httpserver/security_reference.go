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
	group.GET("/business-systems", func(c *gin.Context) {
		filter, ok := securityReferenceFilter(c)
		if !ok {
			return
		}
		result, err := service.SearchBusinessSystems(c.Request.Context(), mustPrincipal(c), filter)
		writeSecurityReferenceResult(c, logger, "list security reference business systems", result, err)
	})
}

func securityReferenceFilter(c *gin.Context) (securityreference.Filter, bool) {
	page, err := parsePageQuery(c)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "invalid_query", "分页参数不正确，page_size 最大为 100")
		return securityreference.Filter{}, false
	}
	return securityreference.Filter{
		Page: page, OrganizationID: strings.TrimSpace(c.Query("organization_id")), Keyword: strings.TrimSpace(c.Query("q")),
	}, true
}

func writeSecurityReferenceResult[T any](c *gin.Context, logger *slog.Logger, operation string, result T, err error) {
	switch {
	case errors.Is(err, securityreference.ErrForbidden):
		writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无集团安全运营参考数据权限")
	case errors.Is(err, securityreference.ErrInvalid):
		writeAPIError(c, http.StatusBadRequest, "invalid_query", "请检查组织编号或搜索条件")
	case err != nil:
		logger.Error(operation, "error", err, "request_id", c.GetString("request_id"))
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "读取安全运营参考数据失败")
	default:
		c.JSON(http.StatusOK, result)
	}
}
