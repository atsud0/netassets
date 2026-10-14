package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/asset"
)

// The recheck endpoint is the only place where an organization formally closes a
// headquarters write. A repeated confirmation must surface as a conflict instead
// of silently succeeding, otherwise a double click or a retrying client would
// overwrite the first decision and write a second audit line. The mapping is
// pinned here because the service errors are shared by three handlers.
func TestDirectChangeRecheckErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "forbidden", err: asset.ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		{name: "invalid", err: asset.ErrInvalid, status: http.StatusBadRequest, code: "invalid_recheck"},
		{name: "not found", err: asset.ErrNotFound, status: http.StatusNotFound, code: "recheck_not_found"},
		{name: "already closed", err: asset.ErrConflict, status: http.StatusConflict, code: "recheck_already_closed"},
		{name: "unexpected", err: errors.New("boom"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			writeDirectChangeRecheckError(ctx, slog.Default(), "confirm direct change recheck task", test.err)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error.Code != test.code {
				t.Fatalf("code = %q, want %q", body.Error.Code, test.code)
			}
		})
	}
}
