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
	"netassets/internal/assetcandidate"
	"netassets/internal/auth"
)

func TestAssetCandidateCreateFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		field   string
		value   any
		message string
	}{
		{"source", "bad", "请选择有效的发现来源"},
		{"asset_type", "bad", "请选择有效的资产类型"},
		{"asset_value", "　 ", "资产值不能为空"},
		{"asset_value", "not-a-domain", "资产值与所选资产类型不匹配，请检查资产值"},
		{"organization_id", "", "请选择建议所属组织"},
		{"organization_id", "bad", "请选择有效的建议所属组织"},
		{"name", strings.Repeat("中", 256), "资产名称最多 255 字"},
		{"source_reference", strings.Repeat("🙂", 256), "来源参考最多 255 字"},
		{"notes", strings.Repeat("a", 5001), "备注最多 5000 字"},
	} {
		for _, permitted := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", tc.field, permitted), func(t *testing.T) {
				p := auth.Principal{}
				if permitted {
					p.Roles = []string{auth.RoleGroupAdmin}
				}
				router := gin.New()
				registerAssetCandidateRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, p); c.Next() }, assetcandidate.NewService(nil, nil), nil, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
				input := map[string]any{"asset_type": "domain", "asset_value": "candidate.example.com", "organization_id": "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a"}
				input[tc.field] = tc.value
				body, _ := json.Marshal(input)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-candidates", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var response struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if !permitted {
					if w.Code != 403 || response.Error.Code != "forbidden" {
						t.Fatalf("permission order: %d %s", w.Code, w.Body.String())
					}
					return
				}
				if w.Code != 400 || response.Error.Code != "invalid_asset_candidate" || response.Error.Message != tc.message {
					t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestAssetCandidateErrorOnlyExposesPublicFieldGuidance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name          string
		err           error
		status        int
		code, message string
	}{
		{"public", &assetcandidate.InputValidationError{Message: "请填写跟进说明"}, 400, "invalid_asset_candidate", "请填写跟进说明"},
		{"internal", fmt.Errorf("PRIVATE_SQL: %w", assetcandidate.ErrInvalid), 400, "invalid_asset_candidate", "请检查资产类型、资产值、所属组织和处理信息"},
		{"forbidden", errors.Join(assetcandidate.ErrForbidden, &assetcandidate.InputValidationError{Message: "PRIVATE_FIELD"}), 403, "forbidden", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/", func(c *gin.Context) {
				writeAssetCandidateError(c, slog.New(slog.NewTextHandler(discardWriter{}, nil)), "test", tc.err)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			var response struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || response.Error.Code != tc.code || (tc.message != "" && response.Error.Message != tc.message) || strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}
