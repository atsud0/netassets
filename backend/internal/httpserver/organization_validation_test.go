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
	"netassets/internal/organization"
)

func TestOrganizationFieldValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerOrganizationRoutes(router.Group("/api/v1"), func(c *gin.Context) {
		c.Set(principalContextKey, auth.Principal{Roles: []string{"group_admin"}})
		c.Next()
	}, &organization.Service{}, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for _, tc := range []struct{ field, value, label, code string }{
			{"name", strings.Repeat("组", 256), "组织名称", "invalid_organization"},
			{"name", "   ", "组织名称", "invalid_organization"},
			{"short_name", strings.Repeat("简", 129), "组织简称", "invalid_organization"},
			{"manager", strings.Repeat("人", 129), "负责人", "invalid_organization"},
			{"notes", strings.Repeat("注", 5001), "备注", "invalid_organization"},
			{"contact", "wrong-phone", "联系方式", "invalid_contact_phone"},
		} {
			t.Run(method+"/"+tc.field+"/"+tc.value[:min(len(tc.value), 6)], func(t *testing.T) {
				data := map[string]string{"code": "OK", "name": "名称", "parent_id": "11111111-1111-4111-8111-111111111111", "type": "department"}
				data[tc.field] = tc.value
				payload, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				path := "/api/v1/organizations"
				if method == http.MethodPatch {
					path += "/11111111-1111-4111-8111-111111111111"
				}
				req := httptest.NewRequest(method, path, bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var result struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != 400 || result.Error.Code != tc.code || !strings.Contains(result.Error.Message, tc.label) {
					t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
