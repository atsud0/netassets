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
	"netassets/internal/inventorytask"
)

func TestInventoryDraftFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		kind, field string
		value       any
		message     string
	}{
		{"asset", "type", "bad", "请选择有效的资产类型"},
		{"asset", "value", "　 ", "资产值不能为空"},
		{"asset", "value", "invalid", "资产值与所选资产类型不匹配，请检查资产值"},
		{"asset", "environment", "bad", "请选择有效的部署环境"},
		{"asset", "network_scope", "bad", "请选择有效的网络范围"},
		{"asset", "status", "bad", "请选择有效的资产状态"},
		{"asset", "importance", "bad", "请选择有效的资产重要程度"},
		{"business-system", "code", "?", "系统编码须为 2–64 位字母、数字、下划线或横线，且以字母或数字开头"},
		{"business-system", "name", "　 ", "系统名称不能为空"},
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
		for _, method := range []string{http.MethodPost, http.MethodPatch} {
			for _, actor := range []string{"permitted", "group_admin", "unpermitted"} {
				t.Run(fmt.Sprintf("%s/%s/%v/%s/%s", tc.kind, tc.field, tc.value, method, actor), func(t *testing.T) {
					p := auth.Principal{}
					if actor != "unpermitted" {
						p.Permissions = []string{"inventory.manage", "asset.manage", "system.manage"}
					}
					if actor == "group_admin" {
						p.Roles = []string{"group_admin"}
					}
					router := gin.New()
					registerInventoryTaskRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }, inventorytask.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
					input := map[string]any{"type": "domain", "value": "input.example.com", "environment": "production", "network_scope": "public", "status": "active", "importance": "normal", "expected_version": 1}
					if tc.kind == "business-system" {
						input = map[string]any{"code": "SYS_INPUT", "name": "字段测试系统", "status": "running", "level": "normal", "internet_exposure": "unknown", "contains_personal_information": "unknown", "contains_sensitive_personal_information": "unknown", "data_volume_over_100k": "unknown", "contains_important_data": "pending", "contains_core_data": "pending", "expected_version": 1}
					}
					input[tc.field] = tc.value
					raw, err := json.Marshal(input)
					if err != nil {
						t.Fatal(err)
					}
					path := "/api/v1/inventory-tasks/task/organizations/11111111-1111-4111-8111-111111111111/" + tc.kind + "-drafts"
					if method == http.MethodPatch {
						path += "/draft"
					}
					req := httptest.NewRequest(method, path, bytes.NewReader(raw))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					expectedStatus := 400
					code := "invalid_asset_draft"
					message := tc.message
					if tc.kind == "business-system" {
						code = "invalid_business_system_draft"
					}
					if actor != "permitted" {
						expectedStatus = 403
						code = "forbidden"
						message = "当前账号无权维护该组织的填报资产草稿"
						if tc.kind == "business-system" {
							message = "当前账号无权维护该组织的填报业务系统草稿"
						}
					}
					var result struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if w.Code != expectedStatus || result.Error.Code != code || result.Error.Message != message {
						t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}

func TestInventoryDraftVersionValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"asset", "business-system"} {
		for _, version := range []any{nil, -1, 0} {
			for _, actor := range []string{"permitted", "group_admin", "unpermitted"} {
				t.Run(fmt.Sprintf("%s/%v/%s", kind, version, actor), func(t *testing.T) {
					p := auth.Principal{}
					if actor != "unpermitted" {
						p.Permissions = []string{"inventory.manage", "asset.manage", "system.manage"}
					}
					if actor == "group_admin" {
						p.Roles = []string{"group_admin"}
					}
					router := gin.New()
					registerInventoryTaskRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }, inventorytask.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
					input := map[string]any{}
					if version != nil {
						input["expected_version"] = version
					}
					raw, err := json.Marshal(input)
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodPatch, "/api/v1/inventory-tasks/task/organizations/11111111-1111-4111-8111-111111111111/"+kind+"-drafts/draft", bytes.NewReader(raw))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var response struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if actor != "permitted" {
						if w.Code != 403 || response.Error.Code != "forbidden" {
							t.Fatalf("permission order changed: HTTP %d %s", w.Code, w.Body.String())
						}
						return
					}
					code := "invalid_asset_draft"
					if kind == "business-system" {
						code = "invalid_business_system_draft"
					}
					if w.Code != 400 || response.Error.Code != code || response.Error.Message != "草稿版本无效，请刷新后重试" {
						t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}

func TestInventoryDraftErrorMessagesExposeOnlyPublicValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"asset", "business-system"} {
		for _, tc := range []struct {
			name          string
			err           error
			status        int
			code, message string
		}{
			{"public field", &inventorytask.InputValidationError{Message: "请选择本组织有效且已启用的负责人"}, 400, "invalid_" + strings.ReplaceAll(kind, "-", "_") + "_draft", "请选择本组织有效且已启用的负责人"},
			{"internal context", fmt.Errorf("PRIVATE_DATABASE_DETAIL: %w", inventorytask.ErrInvalid), 400, "invalid_" + strings.ReplaceAll(kind, "-", "_") + "_draft", ""},
			{"forbidden first", errors.Join(inventorytask.ErrForbidden, &inventorytask.InputValidationError{Message: "PRIVATE_FIELD_DETAIL"}), 403, "forbidden", ""},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				router := gin.New()
				router.GET("/", func(c *gin.Context) {
					logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
					if kind == "asset" {
						writeInventoryAssetDraftError(c, logger, "test", tc.err)
					} else {
						writeInventoryBusinessSystemDraftError(c, logger, "test", tc.err)
					}
				})
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
				var response struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.status || response.Error.Code != tc.code || strings.Contains(w.Body.String(), "PRIVATE_") {
					t.Fatalf("unsafe HTTP %d %s", w.Code, w.Body.String())
				}
				expected := tc.message
				if tc.name == "internal context" {
					expected = "请检查资产字段、所属资源或任务网络范围"
					if kind == "business-system" {
						expected = "请检查业务系统字段和集团自定义字段"
					}
				}
				if expected != "" && response.Error.Message != expected {
					t.Fatalf("message=%q, want %q", response.Error.Message, expected)
				}
			})
		}
	}
}
