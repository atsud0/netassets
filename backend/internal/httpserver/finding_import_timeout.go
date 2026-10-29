package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	findingImportTimeout      = 110 * time.Second
	findingImportWriteTimeout = 2 * time.Minute
)

// An import can create up to 5000 findings, each with its own audit/notification
// transaction. Allow this endpoint to outlive the server's 30s write deadline,
// but stop execution before the client's 120s timeout. Parent cancellation and
// any earlier execution deadline still apply; other routes are unchanged.
func prepareFindingImport(c *gin.Context, logger *slog.Logger) (context.Context, context.CancelFunc) {
	if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(findingImportWriteTimeout)); err != nil {
		logger.Warn("extend finding import write deadline", "error", err, "request_id", c.GetString("request_id"))
	}
	return context.WithTimeout(c.Request.Context(), findingImportTimeout)
}
