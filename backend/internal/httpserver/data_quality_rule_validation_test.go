package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"netassets/internal/auth"
	"netassets/internal/dataquality"
)

func TestDataQualityRuleFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		message string
	}{
		{"blank_name", map[string]any{"rule_name": "　 "}, "规则名称不能为空"},
		{"chinese_name", map[string]any{"rule_name": strings.Repeat("名", 129)}, "规则名称最多 128 字"},
		{"english_name", map[string]any{"rule_name": strings.Repeat("N", 129)}, "规则名称最多 128 字"},
		{"resource", map[string]any{"resource_type": "unknown"}, "请选择有效的检查对象"},
		{"type", map[string]any{"rule_type": "unknown"}, "请选择有效的规则类型"},
		{"severity", map[string]any{"severity": "unknown"}, "请选择有效的问题等级"},
		{"sort_low", map[string]any{"sort_order": -1}, "排序须为 0 至 100000 的整数"},
		{"sort_high", map[string]any{"sort_order": 100001}, "排序须为 0 至 100000 的整数"},
		{"stale_target", map[string]any{"rule_type": "stale", "target_field": "owner_name", "stale_after_days": 1}, "长期未维护规则的检查项须为最后更新时间"},
		{"stale_days", map[string]any{"rule_type": "stale", "target_field": "updated_at", "stale_after_days": 0}, "未维护天数须为 1 至 3650 的整数"},
		{"stale_days_high", map[string]any{"rule_type": "stale", "target_field": "updated_at", "stale_after_days": 3651}, "未维护天数须为 1 至 3650 的整数"},
		{"stale_expiry", map[string]any{"rule_type": "stale", "target_field": "updated_at", "stale_after_days": 1, "expiry_warning_days": 1}, "长期未维护规则不支持提前提醒天数"},
		{"expiry_resource", map[string]any{"rule_type": "expiry", "resource_type": "business_system", "target_field": "expires_at"}, "到期提醒规则的检查对象须为网络资产"},
		{"expiry_target", map[string]any{"rule_type": "expiry", "target_field": "owner_name"}, "请选择有效的到期提醒检查项"},
		{"expiry_stale", map[string]any{"rule_type": "expiry", "target_field": "expires_at", "stale_after_days": 1}, "到期提醒规则不支持未维护天数"},
		{"expiry_days_low", map[string]any{"rule_type": "expiry", "target_field": "expires_at", "expiry_warning_days": -1}, "提前提醒天数须为 0 至 3650 的整数"},
		{"expiry_days_high", map[string]any{"rule_type": "expiry", "target_field": "expires_at", "expiry_warning_days": 3651}, "提前提醒天数须为 0 至 3650 的整数"},
		{"required_stale", map[string]any{"stale_after_days": 1}, "必填检查规则不支持未维护天数"},
		{"required_expiry", map[string]any{"expiry_warning_days": 1}, "必填检查规则不支持提前提醒天数"},
		{"target", map[string]any{"target_field": "unknown"}, "请选择有效的必填检查项"},
		{"condition_field", map[string]any{"conditions": []map[string]any{{"field": "", "operator": "eq", "values": []string{"public"}}}}, "第 1 条生效条件的字段和运算符不能为空，字段最多 128 字节"},
		{"condition_value", map[string]any{"conditions": []map[string]any{{"field": "network_scope", "operator": "eq", "values": []string{"unknown_value"}}}}, "第 1 条生效条件：条件值必须选择当前字段支持的选项"},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPatch} {
			for _, role := range []string{"group_admin", "organization_admin", "security_operator"} {
				t.Run(tc.name+"/"+method+"/"+role, func(t *testing.T) {
					router := gin.New()
					registerDataQualityRuleRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{Roles: []string{role}}); c.Next() }, dataquality.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
					data := map[string]any{"rule_name": "测试规则", "resource_type": "asset", "rule_type": "required", "target_field": "owner_name", "severity": "error"}
					for k, v := range tc.fields {
						data[k] = v
					}
					raw, _ := json.Marshal(data)
					path := "/api/v1/data-quality-rules"
					if method == http.MethodPatch {
						path += "/00000000-0000-0000-0000-000000000001"
					}
					req := httptest.NewRequest(method, path, bytes.NewReader(raw))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var got struct {
						Error struct{ Code, Message string }
					}
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					status, code, message := 400, "invalid_data_quality_request", tc.message
					if role != "group_admin" {
						status, code, message = 403, "forbidden", "仅集团管理员可以配置数据质量规则和执行数据质量检测"
					}
					if w.Code != status || got.Error.Code != code || got.Error.Message != message {
						t.Fatalf("got %d %s; want %d %s %s", w.Code, w.Body.String(), status, code, message)
					}
				})
			}
		}
	}
}

func TestDataQualityRuleErrorRetainsGenericFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"generic_invalid", dataquality.ErrInvalid, http.StatusBadRequest, "请检查规则、筛选条件或问题处理内容"},
		{"database_failure", errors.New("database details must not be exposed"), http.StatusInternalServerError, "处理数据质量规则失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			writeDataQualityRuleError(c, slog.New(slog.NewTextHandler(discardWriter{}, nil)), tc.err)
			var got struct{ Error struct{ Message string } }
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || got.Error.Message != tc.message {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}
