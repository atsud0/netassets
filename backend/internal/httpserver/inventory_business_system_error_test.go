package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/businesssystem"
	"netassets/internal/inventorytask"
)

func TestInventoryBusinessSystemDraftErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
		field  string
	}{
		{"contact", errors.Join(inventorytask.ErrInvalid, businesssystem.ErrInvalidOwnerContact), 400, "invalid_owner_contact", "联系方式"},
		{"wrapped contact", fmt.Errorf("save: %w", errors.Join(inventorytask.ErrInvalid, businesssystem.ErrInvalidOwnerContact)), 400, "invalid_owner_contact", "联系方式"},
		{"generic", inventorytask.ErrInvalid, 400, "invalid_business_system_draft", "集团自定义字段"},
		{"stale version", inventorytask.ErrEditConflict, 409, "draft_edit_conflict", "刷新"},
		{"locked", inventorytask.ErrState, 409, "invalid_state", "任务当前状态"},
		{"forbidden", inventorytask.ErrForbidden, 403, "forbidden", "无权"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPatch, "/inventory-tasks/task/organizations/org/business-system-drafts/draft", nil)
			if !writeInventoryBusinessSystemDraftError(c, slog.Default(), "update draft", tc.err) {
				t.Fatal("error was not handled")
			}
			var response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || response.Error.Code != tc.code || !strings.Contains(response.Error.Message, tc.field) {
				t.Fatalf("unexpected response: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}
