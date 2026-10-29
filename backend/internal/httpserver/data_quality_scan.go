package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	dataQualityScanTimeout      = 100 * time.Second
	dataQualityScanWriteTimeout = 2 * time.Minute
)

// Keep execution below the client's 110s deadline and leave time to return a
// useful error before the write deadline. Only this endpoint gets the longer
// deadline; disconnects and parent cancellation still stop the scan.
func prepareDataQualityScan(c *gin.Context, logger *slog.Logger) (context.Context, context.CancelFunc) {
	if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(dataQualityScanWriteTimeout)); err != nil {
		logger.Warn("extend data quality scan write deadline", "error", err, "request_id", c.GetString("request_id"))
	}
	return context.WithTimeout(c.Request.Context(), dataQualityScanTimeout)
}
