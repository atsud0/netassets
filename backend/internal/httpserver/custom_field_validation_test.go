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
	"netassets/internal/customfield"
)

func TestCustomFieldCreateValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, field string
		value       any
		message     string
	}{
		{"resource", "resource_type", "unknown", "请选择有效的对象类型"},
		{"long_key", "field_key", strings.Repeat("a", 65), "字段编码须为 2–64 位小写字母、数字或下划线，且以字母开头"},
		{"key", "field_key", "x!", "字段编码须为 2–64 位小写字母、数字或下划线，且以字母开头"},
		{"blank", "field_name", "　 ", "字段名称不能为空"},
		{"chinese", "field_name", strings.Repeat("名", 129), "字段名称最多 128 字"},
		{"english", "field_name", strings.Repeat("N", 129), "字段名称最多 128 字"},
		{"type", "field_type", "unknown", "请选择有效的字段类型"},
		{"options", "options", []string{"unexpected"}, "当前字段类型不支持可选项"},
		{"selection", "field_type", "select", "可选项须为 1 至 100 项"},
		{"multiselection", "field_type", "multiselect", "可选项须为 1 至 100 项"},
		{"sort_upper", "sort_order", 100001, "排序须为 0 至 100000 的整数"},
		{"sort", "sort_order", -1, "排序须为 0 至 100000 的整数"},
	} {
		for _, role := range []string{"group_admin", "organization_admin"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				router := gin.New()
				registerCustomFieldRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{Roles: []string{role}}); c.Next() }, customfield.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
				data := map[string]any{"resource_type": "asset", "field_key": "test_field", "field_name": "测试字段", "field_type": "text", "sort_order": 0}
				data[tc.field] = tc.value
				raw, _ := json.Marshal(data)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/custom-fields", bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var result struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				status, code, message := 400, "invalid_custom_field", tc.message
				if role != "group_admin" {
					status, code, message = 403, "forbidden", "仅集团管理员可以配置自定义字段"
				}
				if w.Code != status || result.Error.Code != code || result.Error.Message != message {
					t.Fatalf("response=%d %s, want %d %s %s", w.Code, w.Body.String(), status, code, message)
				}
			})
		}
	}
}
