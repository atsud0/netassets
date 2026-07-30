package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/exportfile"
)

func TestIsCSVExport(t *testing.T) {
	for _, value := range []string{"csv", "CSV", ".csv", " csv "} {
		if !isCSVExport(value) {
			t.Fatalf("isCSVExport(%q) = false", value)
		}
	}
	if isCSVExport("xlsx") {
		t.Fatal("xlsx must not use the CSV streaming path")
	}
}

func TestSetStreamingExportHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	setStreamingExportHeaders(context, "network-assets-ledger.csv", exportfile.Metadata{
		ContentType: exportfile.CSVContentType,
		FileName:    "网络资产台账.csv",
		Count:       123,
	})
	if got := response.Header().Get("Content-Type"); got != exportfile.CSVContentType {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("X-Export-Count"); got != "123" {
		t.Fatalf("X-Export-Count = %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "network-assets-ledger.csv") || !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestClearStreamingExportHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	setStreamingExportHeaders(context, "network-assets-ledger.csv", exportfile.Metadata{
		ContentType: exportfile.CSVContentType,
		FileName:    "网络资产台账.csv",
		Count:       1,
	})
	clearStreamingExportHeaders(context)
	for _, key := range []string{"Content-Type", "Content-Disposition", "X-Export-Count"} {
		if got := response.Header().Get(key); got != "" {
			t.Fatalf("%s still set to %q", key, got)
		}
	}
}
