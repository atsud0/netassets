package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/businesssystem"
)

func TestLedgerFormalFieldMessagesPersistenceAndRejectionAtomicity(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("requires dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	var org, user string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'正式台账字段测试集团','group') RETURNING id::text`, "LEDGER_FIELDS_"+suffix).Scan(&org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, org); err != nil {
			t.Errorf("organization cleanup: %v", err)
		}
	})
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','正式台账字段测试员') RETURNING id::text`, "ledger_fields_"+suffix).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM audit_logs WHERE user_id=$1::uuid`, `DELETE FROM asset_direct_change_recheck_tasks WHERE created_by=$1::uuid`, `DELETE FROM assets WHERE created_by=$1::uuid`, `DELETE FROM business_systems WHERE created_by=$1::uuid`, `DELETE FROM users WHERE id=$1::uuid`} {
			if _, err := pool.Exec(ctx, q, user); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	gin.SetMode(gin.TestMode)
	p := auth.Principal{UserID: user, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{"asset.manage", "system.manage"}}
	actor := p
	router := gin.New()
	requireAuth := func(c *gin.Context) { c.Set(principalContextKey, actor); c.Next() }
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	registerAssetRoutes(router.Group("/api/v1"), requireAuth, asset.NewService(pool), nil, logger)
	registerBusinessSystemRoutes(router.Group("/api/v1"), requireAuth, businesssystem.NewService(pool), nil, logger)
	request := func(t *testing.T, method, path string, input map[string]any, status int, message string) map[string]any {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		var result struct {
			Item  map[string]any
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if message != "" && result.Error.Message != message {
			t.Fatalf("message = %q, want %q", result.Error.Message, message)
		}
		return result.Item
	}
	snapshot := func(t *testing.T) string {
		t.Helper()
		var result string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
   'assets',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM assets a WHERE a.created_by=$1::uuid),
   'systems',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM business_systems b WHERE b.created_by=$1::uuid),
   'audit',(SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid),
   'rechecks',(SELECT count(*) FROM asset_direct_change_recheck_tasks WHERE created_by=$1::uuid),
   'asset_drafts',(SELECT count(*) FROM inventory_asset_drafts WHERE organization_id=$2::uuid),
   'system_drafts',(SELECT count(*) FROM inventory_business_system_drafts WHERE organization_id=$2::uuid))::text`, user, org).Scan(&result)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for index, unit := range []string{"a", "中", "🙂"} {
		for _, kind := range []string{"asset", "business-system"} {
			t.Run(fmt.Sprintf("%s/unicode-%d", kind, index), func(t *testing.T) {
				input := map[string]any{"type": "domain", "value": fmt.Sprintf("ledger-%d-%s.example.com", index, suffix), "organization_id": org, "name": strings.Repeat(unit, 255), "owner_name": strings.Repeat(unit, 128), "environment": "production", "network_scope": "public", "status": "active", "importance": "normal", "direct_change_reason": "字段校验回归"}
				path := "/api/v1/assets"
				if kind == "business-system" {
					path = "/api/v1/business-systems"
					input = map[string]any{"code": fmt.Sprintf("FIELD_%d_%s", index, suffix), "name": strings.Repeat(unit, 255), "owner_name": strings.Repeat(unit, 128), "short_name": strings.Repeat(unit, 128), "deployment_type": strings.Repeat(unit, 32), "organization_id": org, "status": "running", "level": "normal", "internet_exposure": "unknown", "contains_personal_information": "unknown", "contains_sensitive_personal_information": "unknown", "data_volume_over_100k": "unknown", "contains_important_data": "pending", "contains_core_data": "pending"}
				}
				created := request(t, http.MethodPost, path, input, 201, "")
				id := created["id"].(string)
				input["expected_version"] = created["version"]
				updated := request(t, http.MethodPatch, path+"/"+id, input, 200, "")
				if updated["version"].(float64) != created["version"].(float64)+1 {
					t.Fatal("valid update did not advance version")
				}
				input["expected_version"] = updated["version"]
				stored := request(t, http.MethodGet, path+"/"+id, nil, 200, "")
				fields := []string{"name", "owner_name"}
				if kind == "business-system" {
					fields = append(fields, "short_name", "deployment_type")
				}
				for _, field := range fields {
					if created[field] != input[field] || updated[field] != input[field] || stored[field] != input[field] {
						t.Fatalf("valid %s text changed", field)
					}
				}
				cases := []struct {
					field   string
					value   any
					message string
				}{
					{"name", strings.Repeat(unit, 256), "资产名称最多 255 字"},
					{"owner_name", strings.Repeat(unit, 129), "负责人最多 128 字"},
					{"environment", "bad", "请选择有效的部署环境"},
					{"value", "not-a-domain", "资产值与所选资产类型不匹配，请检查资产值"},
				}
				if kind == "business-system" {
					cases = []struct {
						field   string
						value   any
						message string
					}{
						{"name", strings.Repeat(unit, 256), "系统名称最多 255 字"},
						{"short_name", strings.Repeat(unit, 129), "系统简称最多 128 字"},
						{"owner_name", strings.Repeat(unit, 129), "负责人最多 128 字"},
						{"deployment_type", strings.Repeat(unit, 33), "部署方式最多 32 字"},
						{"status", "bad", "请选择有效的系统状态"},
						{"personal_information_record_count", -1, "个人信息记录数量不能小于 0"},
					}
				}
				for _, tc := range cases {
					for _, method := range []string{http.MethodPost, http.MethodPatch} {
						t.Run(tc.field+"/"+method, func(t *testing.T) {
							bad := make(map[string]any, len(input))
							for key, value := range input {
								bad[key] = value
							}
							bad[tc.field] = tc.value
							endpoint := path
							if method == http.MethodPatch {
								endpoint += "/" + id
							}
							before := snapshot(t)
							request(t, method, endpoint, bad, 400, tc.message)
							if snapshot(t) != before {
								t.Fatal("rejected input changed ledger, version, audit, recheck or drafts")
							}
							actor = auth.Principal{UserID: user}
							request(t, method, endpoint, bad, 403, "")
							actor = p
							if snapshot(t) != before {
								t.Fatal("unpermitted input changed data")
							}
						})
					}
				}
			})
		}
	}
}
