package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/securityworkorder"
)

type securityBatchAction func(context.Context, auth.Principal, securityworkorder.BatchInput, string, string) (securityworkorder.BatchResult, error)

func registerSecurityBatchRoute(group *gin.RouterGroup, method, path string, action securityBatchAction, logger *slog.Logger) {
	group.Handle(method, path, func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		var input securityworkorder.BatchInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAPIError(c, http.StatusBadRequest, "invalid_request", "请检查批量操作内容，单次最多 200 项")
			return
		}
		result, err := action(c.Request.Context(), mustPrincipal(c), input, c.GetString("request_id"), c.ClientIP())
		switch {
		case errors.Is(err, securityworkorder.ErrForbidden):
			writeAPIError(c, http.StatusForbidden, "forbidden", "当前账号无批量操作权限")
		case errors.Is(err, securityworkorder.ErrInvalid):
			writeAPIError(c, http.StatusBadRequest, "invalid_batch", securityWorkOrderValidationMessage(err, "请检查批量操作内容，单次最多 200 项"))
		case err != nil:
			logger.Error("security batch action", "error", err, "request_id", c.GetString("request_id"))
			writeAPIError(c, http.StatusInternalServerError, "internal_error", "批量操作失败")
		default:
			c.JSON(http.StatusOK, result)
		}
	})
}
