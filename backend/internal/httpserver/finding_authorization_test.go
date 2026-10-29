package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/auth"
	"netassets/internal/securityworkorder"
)

func TestFindingMutationsRejectUnauthorizedBeforeLookupHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, p := range []auth.Principal{
		{Roles: []string{"organization_admin"}},
		{Roles: []string{"organization_admin"}, Permissions: []string{auth.PermissionSecurityFinding}},
		{Roles: []string{auth.RoleSecurityOperator}},
		{Roles: []string{auth.RoleGroupAdmin}},
	} {
		router := gin.New()
		registerSecurityWorkOrderRoutes(router.Group("/api/v1"), func(c *gin.Context) {
			c.Set(principalContextKey, p)
			c.Next()
		}, securityworkorder.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
		for _, id := range []string{"not-a-uuid", "11111111-1111-4111-8111-111111111111"} {
			for _, action := range []string{"accept-risk", "revoke-risk", "review-risk", "update"} {
				t.Run(strings.Join(p.Roles, ",")+"/"+strings.Join(p.Permissions, ",")+"/"+id+"/"+action, func(t *testing.T) {
					path, method := "/api/v1/security-findings/"+id+"/"+action, http.MethodPost
					if action == "update" {
						path, method = "/api/v1/security-findings/"+id, http.MethodPatch
					}
					// Well-formed JSON passes the HTTP binding layer but has no business fields.
					req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					var result struct{ Error struct{ Code string } }
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if w.Code != http.StatusForbidden || result.Error.Code != "forbidden" {
						t.Fatalf("got %d %s, want 403 forbidden", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}
