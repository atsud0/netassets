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
	"time"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/inventorytask"
)

func TestInventoryTaskFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	many := func(n int) []string {
		values := make([]string, n)
		for i := range values {
			values[i] = fmt.Sprintf("value-%d", i)
		}
		return values
	}
	cases := []struct {
		name, field string
		value       any
		message     string
		preview     bool
	}{
		{"blank_name", "name", "　 ", "任务名称不能为空", false},
		{"long_chinese_name", "name", strings.Repeat("填", 256), "任务名称最多 255 字", false},
		{"long_english_name", "name", strings.Repeat("T", 256), "任务名称最多 255 字", false},
		{"long_description", "description", strings.Repeat("说", 5001), "任务说明最多 5000 字", false},
		{"missing_start", "start_at", time.Time{}, "请选择开始时间", false},
		{"missing_deadline", "deadline_at", time.Time{}, "请选择截止时间", false},
		{"equal_deadline", "deadline_at", start, "截止时间必须晚于开始时间", false},
		{"earlier_deadline", "deadline_at", start.Add(-time.Hour), "截止时间必须晚于开始时间", false},
		{"status", "status", "closed", "任务状态仅支持草稿或进行中", false},
		{"scope", "asset_network_scope", "unknown", "请选择有效的网络资产核查范围", true},
		{"empty_organizations", "organization_ids", []string{" ", "　"}, "请选择至少一个填报组织", true},
		{"many_organizations", "organization_ids", many(1001), "填报组织最多选择 1000 个", true},
		{"many_fields", "required_fields", many(101), "填报内容最多选择 100 项", true},
		{"invalid_field", "required_fields", []string{"network_assets", "unknown"}, "请选择有效的填报内容", true},
		{"empty_fields", "required_fields", []string{}, "请至少选择业务系统、数据安全属性或网络资产中的一项填报内容", true},
		{"no_resources", "required_fields", []string{"owner_contacts"}, "请至少选择业务系统、数据安全属性或网络资产中的一项填报内容", true},
		{"many_quality_rules", "quality_rules", many(101), "质量规则最多选择 100 项", false},
	}
	for _, tc := range cases {
		paths := []string{"/api/v1/inventory-tasks"}
		if tc.preview {
			paths = append(paths, "/api/v1/inventory-tasks/scope-preview")
		}
		for _, path := range paths {
			for _, role := range []string{"group_admin", "organization_admin", "security_operator", "asset_operator"} {
				t.Run(tc.name+"/"+path+"/"+role, func(t *testing.T) {
					router := gin.New()
					registerInventoryTaskRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{Roles: []string{role}}); c.Next() }, inventorytask.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
					data := map[string]any{"name": "合法填报任务", "start_at": start, "deadline_at": start.Add(time.Hour), "status": "draft", "organization_ids": []string{"00000000-0000-0000-0000-000000000001"}, "required_fields": []string{"network_assets"}, "asset_network_scope": "all"}
					data[tc.field] = tc.value
					raw, err := json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var got struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					status, code, message := 400, "invalid_inventory_task", tc.message
					if strings.HasSuffix(path, "scope-preview") {
						code = "invalid_inventory_task_scope"
					}
					if role != "group_admin" {
						status, code, message = 403, "forbidden", "仅集团管理员可以创建填报任务"
						if strings.HasSuffix(path, "scope-preview") {
							message = "仅集团管理员可以预览填报任务范围"
						}
					}
					if w.Code != status || got.Error.Code != code || got.Error.Message != message {
						t.Fatalf("got %d %s; want %d %s %s", w.Code, w.Body.String(), status, code, message)
					}
				})
			}
		}
	}
}

func TestInventoryTaskValidationMessageDoesNotExposeInternalErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{inventorytask.ErrInvalid, "legacy fallback"},
		{fmt.Errorf("wrapped: %w", inventorytask.ErrInvalid), "legacy fallback"},
		{errors.New("database connection detail"), "legacy fallback"},
		{fmt.Errorf("wrapped: %w", &inventorytask.InputValidationError{Message: "任务名称不能为空"}), "任务名称不能为空"},
	} {
		if got := inventoryTaskValidationMessage(tc.err, "legacy fallback"); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}
