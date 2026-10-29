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
	"netassets/internal/securityworkorder"
)

func TestSecurityCreateFieldMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	for _, finding := range []bool{true, false} {
		kind, code, fallback := "finding", "invalid_finding", "请检查责任组织、关联资产、严重等级和漏洞信息"
		if !finding {
			kind, code, fallback = "work_order", "invalid_work_order", "请检查责任组织、关联漏洞、整改期限和操作内容"
		}
		for _, tc := range []struct {
			name   string
			err    error
			want   string
			status int
		}{
			{"field", &securityworkorder.InputValidationError{Message: "字段最多 255 字"}, "字段最多 255 字", 400},
			{"wrapped field", fmt.Errorf("private SQL: %w", &securityworkorder.InputValidationError{Message: "字段不能为空"}), "字段不能为空", 400},
			{"generic", securityworkorder.ErrInvalid, fallback, 400},
			{"wrapped generic", fmt.Errorf("private SQL: %w", securityworkorder.ErrInvalid), fallback, 400},
			{"internal", errors.New("private SQL"), "", 500},
			{"forbidden before field", errors.Join(securityworkorder.ErrForbidden, &securityworkorder.InputValidationError{Message: "字段细节"}), "", 403},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				if finding {
					writeFindingResult(c, logger, "test", http.StatusCreated, securityworkorder.Finding{}, tc.err)
				} else {
					writeWorkOrderResult(c, logger, "test", http.StatusCreated, securityworkorder.WorkOrder{}, tc.err)
				}
				if recorder.Code != tc.status {
					t.Fatalf("status %d, want %d", recorder.Code, tc.status)
				}
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if tc.want != "" && (body.Error.Message != tc.want || body.Error.Code != code) {
					t.Errorf("error = %+v, want %s / %s", body.Error, code, tc.want)
				}
				if strings.Contains(recorder.Body.String(), "private SQL") || (tc.status == 403 && strings.Contains(recorder.Body.String(), "字段细节")) {
					t.Fatal("private or unauthorized detail exposed")
				}
			})
		}
	}
}
