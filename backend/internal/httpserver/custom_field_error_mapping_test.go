package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/customfield"
)

func TestCustomFieldForbiddenIsHTTP403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPut, "/assets/test/custom-fields", nil)
			err := customfield.ErrForbidden
			if wrapped {
				err = fmt.Errorf("save formal values: %w", err)
			}
			writeCustomFieldError(c, slog.New(slog.NewTextHandler(discardWriter{}, nil)), err)
			if w.Code != http.StatusForbidden {
				t.Fatalf("HTTP status = %d, want 403: %s", w.Code, w.Body.String())
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != "forbidden" {
				t.Fatalf("error code = %q, want forbidden", response.Error.Code)
			}
		})
	}
}
