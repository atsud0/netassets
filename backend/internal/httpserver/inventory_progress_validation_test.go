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
	"netassets/internal/inventorytask"
)

func TestInventoryProgressValidationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path string
		input      any
		message    string
	}{
		{"submit", "submit", inventorytask.SubmissionInput{Note: strings.Repeat("交", 5001)}, "提交说明最多 5000 字"},
		{"decision", "review", inventorytask.ReviewInput{Decision: "other"}, "请选择有效的审核结果"},
		{"blank return", "review", inventorytask.ReviewInput{Decision: "returned", ReturnReason: "　 "}, "退回原因不能为空"},
		{"long return", "review", inventorytask.ReviewInput{Decision: "returned", ReturnReason: strings.Repeat("退", 2001)}, "退回原因最多 2000 字"},
		{"long comment", "review", inventorytask.ReviewInput{Decision: "returned", ReturnReason: "整改", Comments: []inventorytask.ReviewCommentInput{{ResourceType: "network_asset", ResourceID: "resource", Comment: strings.Repeat("R", 2001)}}}, "逐条审核意见最多 2000 字"},
	} {
		for _, role := range []string{"group_admin", "organization_admin"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				router := gin.New()
				registerInventoryTaskRoutes(router.Group("/api/v1"), func(c *gin.Context) { c.Set(principalContextKey, auth.Principal{Roles: []string{role}}); c.Next() }, inventorytask.NewService(nil), slog.New(slog.NewTextHandler(discardWriter{}, nil)))
				raw, err := json.Marshal(tc.input)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory-tasks/task/organizations/org/"+tc.path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				denied := (tc.path == "submit" && role == "group_admin") || (tc.path == "review" && role != "group_admin")
				expectedStatus := 400
				expectedCode := "invalid_input"
				expectedMessage := tc.message
				if denied {
					expectedStatus = 403
					expectedCode = "forbidden"
					expectedMessage = "您没有权限执行该操作"
				}
				var result struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != expectedStatus || result.Error.Code != expectedCode || result.Error.Message != expectedMessage {
					t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
