package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/inventorytask"
)

func TestResourceRestoreEditConflictIsHTTP409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/restore", nil)
	if !writeResourceSubmissionRestoreError(c, slog.New(slog.NewTextHandler(discardWriter{}, nil)), fmt.Errorf("restore: %w", inventorytask.ErrEditConflict)) {
		t.Fatal("conflict was not handled")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "draft_edit_conflict" {
		t.Fatalf("error code=%s", response.Error.Code)
	}
}
