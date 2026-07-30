package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"netassets/internal/exportfile"
)

const exportWriteTimeout = 10 * time.Minute

// extendExportWriteDeadline overrides the server-wide 30 second write deadline
// only for download endpoints. A finite deadline avoids allowing slow clients to
// hold a connection forever while giving large streamed CSV exports enough time.
func extendExportWriteDeadline(c *gin.Context, logger *slog.Logger) {
	if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(exportWriteTimeout)); err != nil {
		logger.Warn("extend export write deadline", "error", err, "request_id", c.GetString("request_id"))
	}
}

func setStreamingExportHeaders(c *gin.Context, fallbackFileName string, meta exportfile.Metadata) {
	c.Header("Content-Type", meta.ContentType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s; filename*=UTF-8''%s", fallbackFileName, url.PathEscape(meta.FileName)))
	c.Header("X-Export-Count", fmt.Sprintf("%d", meta.Count))
}

func clearStreamingExportHeaders(c *gin.Context) {
	c.Writer.Header().Del("Content-Type")
	c.Writer.Header().Del("Content-Disposition")
	c.Writer.Header().Del("X-Export-Count")
}

func isCSVExport(format string) bool {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".") == "csv"
}
