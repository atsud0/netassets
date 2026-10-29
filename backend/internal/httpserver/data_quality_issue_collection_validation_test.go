package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/dataquality"
)

func TestDataQualityIssueCollectionFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const id = "00000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		name, method, path, code, message string
		data                              map[string]any
	}{
		{"empty_selection", "POST", "/batch", "invalid_batch", "请至少选择 1 个数据质量问题", map[string]any{"action": "submit", "note": "说明"}},
		{"too_many", "POST", "/batch", "invalid_batch", "单次最多选择 200 个数据质量问题", map[string]any{"ids": strings.Fields(strings.Repeat(id+" ", 201)), "action": "submit", "note": "说明"}},
		{"bad_id", "POST", "/batch", "invalid_batch", "数据质量问题标识格式不正确", map[string]any{"ids": []string{"bad"}, "action": "submit", "note": "说明"}},
		{"bad_action", "POST", "/batch", "invalid_batch", "请选择有效的批量操作", map[string]any{"ids": []string{id}, "action": "review"}},
		{"missing_assignee", "POST", "/batch", "invalid_batch", "请选择有效的整改负责人", map[string]any{"ids": []string{id}, "action": "assign"}},
		{"bad_assignee", "POST", "/batch", "invalid_batch", "请选择有效的整改负责人", map[string]any{"ids": []string{id}, "action": "start", "assignee_user_id": "bad"}},
		{"blank_note", "POST", "/batch", "invalid_batch", "处理说明不能为空", map[string]any{"ids": []string{id}, "action": "submit", "note": "　 "}},
		{"long_note", "POST", "/batch", "invalid_batch", "处理说明最多 5000 字", map[string]any{"ids": []string{id}, "action": "submit", "note": strings.Repeat("中", 5001)}},
		{"organization_filter", "GET", "?organization_id=bad", "invalid_data_quality_issue_request", "组织筛选标识格式不正确", nil},
		{"status_filter", "GET", "?status=bad", "invalid_data_quality_issue_request", "请选择有效的问题状态", nil},
		{"severity_filter", "GET", "?severity=bad", "invalid_data_quality_issue_request", "请选择有效的严重程度", nil},
		{"resource_filter", "GET", "?resource_type=bad", "invalid_data_quality_issue_request", "请选择有效的检查对象", nil},
		{"assignee_filter", "GET", "?assignee=bad", "invalid_data_quality_issue_request", "整改负责人筛选条件格式不正确", nil},
		{"detail_id", "GET", "/bad", "invalid_data_quality_issue_request", "数据质量问题标识格式不正确", nil},
		{"events_id", "GET", "/bad/events", "invalid_data_quality_issue_request", "数据质量问题标识格式不正确", nil},
		{"assignees_id", "GET", "/bad/assignees", "invalid_data_quality_issue_request", "数据质量问题标识格式不正确", nil},
	} {
		for _, role := range []string{"group_admin", "quality_operator", "asset_operator"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				p := auth.Principal{UserID: id, Roles: []string{role}}
				if role == "quality_operator" {
					p.Permissions = []string{"data_quality.manage"}
				}
				router := gin.New()
				registerDataQualityRuleRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }, dataquality.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
				raw, err := json.Marshal(tc.data)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(tc.method, "/api/v1/data-quality-issues"+tc.path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var got struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				status, code, message := http.StatusBadRequest, tc.code, tc.message
				if role == "asset_operator" {
					status, code, message = http.StatusForbidden, "forbidden", "当前账号无权处理该组织的数据质量问题"
				}
				if w.Code != status || got.Error.Code != code || got.Error.Message != message {
					t.Fatalf("got %d %s; want %d %s %s", w.Code, w.Body.String(), status, code, message)
				}
			})
		}
	}
}
