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
	"netassets/internal/securitytestbatch"
)

func TestSecurityTestBatchTransitionFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{auth.RoleGroupAdmin, auth.RoleSecurityOperator, "organization_admin"} {
		router := gin.New()
		registerSecurityTestBatchRoutes(router.Group("/api/v1"), func(c *gin.Context) {
			c.Set(principalContextKey, auth.Principal{Roles: []string{role}, Permissions: []string{auth.PermissionSecurityTest}})
			c.Next()
		}, securitytestbatch.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
		for _, action := range []string{"authorize", "start", "complete", "cancel"} {
			label := "操作说明"
			if action == "cancel" {
				label = "取消原因"
			}
			cases := []struct{ comment, message string }{{strings.Repeat("测", 2001), label + "最多 2000 字"}}
			if action == "cancel" {
				cases = append(cases, struct{ comment, message string }{"   ", "取消原因不能为空"})
			}
			for _, tc := range cases {
				t.Run(role+"/"+action+"/"+tc.message, func(t *testing.T) {
					payload, err := json.Marshal(map[string]string{"comment": tc.comment})
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodPost, "/api/v1/security-test-batches/11111111-1111-4111-8111-111111111111/"+action, bytes.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var result struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					status, code, message := 400, "invalid_security_test_batch", tc.message
					if role == "organization_admin" {
						status, code, message = 403, "forbidden", "当前账号无安全测试管理权限"
					}
					if w.Code != status || result.Error.Code != code || result.Error.Message != message {
						t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}

func TestSecurityTestBatchInputFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{auth.RoleGroupAdmin, auth.RoleSecurityOperator, "organization_admin"} {
		router := gin.New()
		registerSecurityTestBatchRoutes(router.Group("/api/v1"), func(c *gin.Context) {
			c.Set(principalContextKey, auth.Principal{Roles: []string{role}, Permissions: []string{auth.PermissionSecurityTest}})
			c.Next()
		}, securitytestbatch.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
		for _, tc := range []struct {
			field   string
			value   any
			message string
		}{
			{"batch_name", "　 ", "批次名称不能为空"}, {"batch_name", strings.Repeat("名", 256), "批次名称最多 255 字"},
			{"provider", "", "测试单位不能为空"}, {"provider", strings.Repeat("P", 256), "测试单位最多 255 字"},
			{"authorization_note", "", "授权说明不能为空"}, {"authorization_note", strings.Repeat("授", 20001), "授权说明最多 20000 字"},
			{"notes", strings.Repeat("😀", 20001), "测试约束最多 20000 字"},
			{"test_type", "unknown", "请选择有效的测试类型"},
			{"start_at", "0001-01-01T00:00:00Z", "请选择计划开始时间"},
			{"end_at", "2020-01-01T08:00:00Z", "计划结束时间必须晚于计划开始时间"},
			{"asset_ids", []string{}, "请至少选择 1 项公网资产"}, {"asset_ids", []string{"invalid"}, "公网资产范围包含无效的资产标识"},
		} {
			for _, method := range []string{http.MethodPost, http.MethodPatch} {
				t.Run(role+"/"+method+"/"+tc.field+"/"+tc.message, func(t *testing.T) {
					body := map[string]any{"batch_name": "批次", "test_type": "manual", "provider": "单位", "authorization_note": "授权", "start_at": "2020-01-01T08:00:00Z", "end_at": "2020-01-02T08:00:00Z", "asset_ids": []string{"11111111-1111-4111-8111-111111111111"}}
					body[tc.field] = tc.value
					payload, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					path := "/api/v1/security-test-batches"
					if method == http.MethodPatch {
						path += "/11111111-1111-4111-8111-111111111111"
					}
					req := httptest.NewRequest(method, path, bytes.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var result struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					status, code, message := 400, "invalid_security_test_batch", tc.message
					if role == "organization_admin" {
						status, code, message = 403, "forbidden", "当前账号无安全测试管理权限"
					}
					if w.Code != status || result.Error.Code != code || result.Error.Message != message {
						t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}
