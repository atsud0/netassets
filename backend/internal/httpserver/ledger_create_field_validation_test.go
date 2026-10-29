package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/businesssystem"
)

// Exercise the actual create routes: shared preparation errors must not be lost
// when used outside an inventory task. Invalid inputs never need a database.
func TestLedgerCreateFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		kind, field string
		value       any
		message     string
	}{
		{"asset", "organization_id", "　 ", "请选择所属组织"},
		{"asset", "type", "bad", "请选择有效的资产类型"},
		{"asset", "value", "　 ", "资产值不能为空"},
		{"asset", "value", "not-a-domain", "资产值与所选资产类型不匹配，请检查资产值"},
		{"asset", "environment", "bad", "请选择有效的部署环境"},
		{"asset", "network_scope", "bad", "请选择有效的网络范围"},
		{"asset", "status", "bad", "请选择有效的资产状态"},
		{"asset", "importance", "bad", "请选择有效的资产重要程度"},
		{"asset", "name", strings.Repeat("🙂", 256), "资产名称最多 255 字"},
		{"asset", "owner_name", strings.Repeat("中", 129), "负责人最多 128 字"},
		{"business-system", "code", "?", "系统编码须为 2–64 位字母、数字、下划线或横线，且以字母或数字开头"},
		{"business-system", "name", "　 ", "系统名称不能为空"},
		{"business-system", "name", strings.Repeat("中", 256), "系统名称最多 255 字"},
		{"business-system", "short_name", strings.Repeat("🙂", 129), "系统简称最多 128 字"},
		{"business-system", "owner_name", strings.Repeat("中", 129), "负责人最多 128 字"},
		{"business-system", "deployment_type", strings.Repeat("a", 33), "部署方式最多 32 字"},
		{"business-system", "organization_id", "", "请选择所属组织"},
		{"business-system", "owner_contact", "bad", "联系方式格式不正确，请填写手机号码、座机或分机号"},
		{"business-system", "status", "bad", "请选择有效的系统状态"},
		{"business-system", "level", "bad", "请选择有效的系统等级"},
		{"business-system", "internet_exposure", "bad", "请选择有效的互联网暴露情况"},
		{"business-system", "contains_personal_information", "bad", "请选择有效的是否包含个人信息选项"},
		{"business-system", "contains_sensitive_personal_information", "bad", "请选择有效的是否包含敏感个人信息选项"},
		{"business-system", "personal_information_record_count", -1, "个人信息记录数量不能小于 0"},
		{"business-system", "data_volume_over_100k", "bad", "请选择有效的数据量是否超过 10 万条选项"},
		{"business-system", "contains_important_data", "bad", "请选择有效的是否包含重要数据选项"},
		{"business-system", "contains_core_data", "bad", "请选择有效的是否包含核心数据选项"},
	} {
		for _, actor := range []string{"group", "editor", "unpermitted"} {
			t.Run(fmt.Sprintf("%s/%s/%s/%s", tc.kind, tc.field, tc.message, actor), func(t *testing.T) {
				p := auth.Principal{}
				if actor != "unpermitted" {
					p.Permissions = []string{"asset.manage", "system.manage"}
				}
				if actor == "group" {
					p.Roles = []string{auth.RoleGroupAdmin}
				}
				router := gin.New()
				requireAuth := func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }
				logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
				input := map[string]any{"type": "domain", "value": "input.example.com", "organization_id": "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", "environment": "production", "network_scope": "public", "status": "active", "importance": "normal", "direct_change_reason": "字段校验回归"}
				path, code := "/api/v1/assets", "invalid_asset"
				if tc.kind == "asset" {
					registerAssetRoutes(router.Group("/api/v1"), requireAuth, asset.NewService(nil), nil, logger)
				} else {
					registerBusinessSystemRoutes(router.Group("/api/v1"), requireAuth, businesssystem.NewService(nil), nil, logger)
					input = map[string]any{"code": "FIELD_INPUT", "name": "字段测试系统", "organization_id": "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", "status": "running", "level": "normal", "internet_exposure": "unknown", "contains_personal_information": "unknown", "contains_sensitive_personal_information": "unknown", "data_volume_over_100k": "unknown", "contains_important_data": "pending", "contains_core_data": "pending"}
					path, code = "/api/v1/business-systems", "invalid_business_system"
					if tc.field == "owner_contact" {
						code = "invalid_owner_contact"
					}
				}
				input[tc.field] = tc.value
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var result struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if actor == "unpermitted" {
					if w.Code != 403 || result.Error.Code != "forbidden" {
						t.Fatalf("permission order: HTTP %d %s", w.Code, w.Body.String())
					}
					return
				}
				if w.Code != 400 || result.Error.Code != code || result.Error.Message != tc.message {
					t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
