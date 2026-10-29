package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/dataquality"
)

func TestDataQualityIssueActionFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const userID = "00000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		name, action string
		data         map[string]any
		message      string
		groupOnly    bool
	}{
		{"assign_missing", "assign", map[string]any{}, "请选择有效的整改负责人", false},
		{"start_invalid_assignee", "start", map[string]any{"assignee_user_id": "bad"}, "请选择有效的整改负责人", false},
		{"assign_note", "assign", map[string]any{"assignee_user_id": userID, "note": strings.Repeat("调", 5001)}, "处理说明最多 5000 字", false},
		{"start_note", "start", map[string]any{"note": strings.Repeat("S", 5001)}, "处理说明最多 5000 字", false},
		{"submit_blank", "submit", map[string]any{"note": "　 "}, "处理说明不能为空", false},
		{"submit_note", "submit", map[string]any{"note": strings.Repeat("整", 5001)}, "处理说明最多 5000 字", false},
		{"review_blank", "review", map[string]any{"approved": false, "note": "　 "}, "退回整改时处理说明不能为空", true},
		{"review_note", "review", map[string]any{"approved": true, "note": strings.Repeat("R", 5001)}, "处理说明最多 5000 字", true},
		{"exempt_blank", "exempt", map[string]any{"reason": "　 "}, "豁免原因不能为空", true},
		{"exempt_reason", "exempt", map[string]any{"reason": strings.Repeat("豁", 5001)}, "豁免原因最多 5000 字", true},
		{"exempt_date", "exempt", map[string]any{"reason": "合法原因", "expires_at": "bad"}, "豁免有效期格式不正确", true},
		{"exempt_past", "exempt", map[string]any{"reason": "合法原因", "expires_at": "2020-01-01T00:00:00Z"}, "豁免有效期必须晚于当前时间", true},
		{"reopen_note", "reopen", map[string]any{"note": strings.Repeat("重", 5001)}, "处理说明最多 5000 字", true},
	} {
		for _, role := range []string{"group_admin", "quality_operator", "asset_operator"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				p := auth.Principal{UserID: userID, Roles: []string{role}}
				if role == "quality_operator" {
					p.Permissions = []string{"data_quality.manage"}
				}
				router := gin.New()
				registerDataQualityRuleRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }, dataquality.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
				raw, err := json.Marshal(tc.data)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/data-quality-issues/"+userID+"/"+tc.action, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var got struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				status, code, message := 400, "invalid_data_quality_issue_request", tc.message
				if role == "asset_operator" || (tc.groupOnly && role != "group_admin") {
					status, code, message = 403, "forbidden", "当前账号无权处理该组织的数据质量问题"
				}
				if w.Code != status || got.Error.Code != code || got.Error.Message != message {
					t.Fatalf("got %d %s; want %d %s %s", w.Code, w.Body.String(), status, code, message)
				}
			})
		}
	}
}

func TestDataQualityIssueValidationFallbackIsSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{dataquality.ErrInvalid, 400, "请检查问题筛选条件、整改负责人或处理说明"},
		{fmt.Errorf("wrapped: %w", dataquality.ErrInvalid), 400, "请检查问题筛选条件、整改负责人或处理说明"},
		{errors.New("private database detail"), 500, "处理数据质量问题失败"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeDataQualityIssueError(c, slog.New(slog.NewTextHandler(discardWriter{}, nil)), tc.err)
		var got struct{ Error struct{ Message string } }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || got.Error.Message != tc.message {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	}
}
