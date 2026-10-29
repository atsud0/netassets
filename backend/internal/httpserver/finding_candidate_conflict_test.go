package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/securityworkorder"
)

func TestFindingCandidateLinkConflictResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"target mismatch", securityworkorder.ErrConflict, http.StatusConflict, "candidate_link_conflict", "待关联漏洞与目标资产或工作版本不匹配，请刷新后重新选择"},
		{"wrapped retry mismatch", fmt.Errorf("retry release: %w", securityworkorder.ErrConflict), http.StatusConflict, "candidate_link_conflict", "待关联漏洞与目标资产或工作版本不匹配，请刷新后重新选择"},
		{"already settled", securityworkorder.ErrState, http.StatusConflict, "invalid_candidate_state", "该待关联漏洞已处理，请刷新列表"},
		{"unexpected database failure", errors.New("unavailable"), http.StatusInternalServerError, "internal_error", "处理漏洞导入待关联项失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeFindingImportCandidateError(c, logger, "test", tc.err)
			if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), `"code":"`+tc.code+`"`) || !strings.Contains(recorder.Body.String(), tc.message) {
				t.Fatalf("response = %d %s, want %d %s %s", recorder.Code, recorder.Body.String(), tc.status, tc.code, tc.message)
			}
		})
	}
}
