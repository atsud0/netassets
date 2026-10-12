package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/securityworkorder"
)

// The service layer decides *whether* a caller may act; these writers decide what
// the caller is told. A permission denial that surfaces as 500, or an individual
// work-order rejection that surfaces as 400, is a real leak of internal state and
// breaks the documented 403/409 contract. The mapping is asserted directly so it
// cannot drift while the underlying authorization checks change.

// securityWorkOrderFixture exercises every error writer on the security work-order
// and finding surfaces with one injected error.
type securityWorkOrderFixture struct {
	name    string
	handler func(*gin.Context)
	errors  map[string]error
}

func securityWorkOrderErrorFixtures() []securityWorkOrderFixture {
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	workOrderWriter := func(status int) func(*gin.Context) {
		return func(c *gin.Context) {
			writeWorkOrderResult(c, logger, "test", status, securityworkorder.WorkOrder{}, currentError(c))
		}
	}
	attachmentWriter := func(status int) func(*gin.Context) {
		return func(c *gin.Context) {
			writeAttachmentResult(c, logger, "test", status, securityworkorder.Attachment{}, currentError(c))
		}
	}
	findingWriter := func(status int) func(*gin.Context) {
		return func(c *gin.Context) {
			writeFindingResult(c, logger, "test", status, securityworkorder.Finding{}, currentError(c))
		}
	}
	riskWriter := func(c *gin.Context) {
		writeFindingRiskResult(c, logger, "test", securityworkorder.Finding{}, currentError(c))
	}
	childWriter := func(c *gin.Context) {
		writeWorkOrderChildError(c, logger, "test", "读取安全工单处理记录失败", currentError(c))
	}
	candidateWriter := func(c *gin.Context) {
		writeFindingImportCandidateError(c, logger, "test", currentError(c))
	}
	importWriter := func(c *gin.Context) {
		writeFindingImportResult(c, logger, securityworkorder.FindingImportResult{}, currentError(c))
	}
	return []securityWorkOrderFixture{
		{
			name:    "work order",
			handler: workOrderWriter(http.StatusOK),
			errors: map[string]error{
				"forbidden":      securityworkorder.ErrForbidden,
				"invalid":        securityworkorder.ErrInvalid,
				"not_found":      securityworkorder.ErrNotFound,
				"invalid_state":  securityworkorder.ErrState,
				"individual_409": securityworkorder.ErrIndividualWorkOrderAction,
				"conflict":       securityworkorder.ErrConflict,
			},
		},
		{
			name:    "work order child",
			handler: childWriter,
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
			},
		},
		{
			name:    "attachment",
			handler: attachmentWriter(http.StatusCreated),
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
			},
		},
		{
			name:    "finding",
			handler: findingWriter(http.StatusOK),
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
			},
		},
		{
			name:    "risk",
			handler: riskWriter,
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
				"conflict":  securityworkorder.ErrConflict,
			},
		},
		{
			name:    "import candidate",
			handler: candidateWriter,
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
				"conflict":  securityworkorder.ErrState,
			},
		},
		{
			name:    "import",
			handler: importWriter,
			errors: map[string]error{
				"forbidden": securityworkorder.ErrForbidden,
				"invalid":   securityworkorder.ErrInvalid,
				"not_found": securityworkorder.ErrNotFound,
				"conflict":  securityworkorder.ErrState,
			},
		},
	}
}

// currentError reads the error the test injected for this request. Using the gin
// context keeps the fixture table readable: the handler under test reads the
// error exactly the way production code passes it in.
func currentError(c *gin.Context) error {
	value, _ := c.Get(errorContextKey)
	if err, ok := value.(error); ok {
		return err
	}
	return nil
}

const errorContextKey = "test_injected_error"

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestSecurityWorkOrderErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expected := map[string]int{
		"forbidden":      http.StatusForbidden,
		"invalid":        http.StatusBadRequest,
		"not_found":      http.StatusNotFound,
		"conflict":       http.StatusConflict,
		"invalid_state":  http.StatusConflict,
		"individual_409": http.StatusConflict,
	}
	for _, fixture := range securityWorkOrderErrorFixtures() {
		for label, injected := range fixture.errors {
			want := expected[label]
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/security-work-orders/x", nil)
			context.Set(errorContextKey, injected)
			fixture.handler(context)
			if recorder.Code != want {
				t.Errorf("%s writer with %s error = status %d, want %d", fixture.name, label, recorder.Code, want)
			}
			if recorder.Code >= http.StatusInternalServerError {
				t.Errorf("%s writer with %s error exposed status %d; a denial must never surface as a server error", fixture.name, label, recorder.Code)
			}
		}
	}
}

// A denied request must not carry a success payload shape; the client has to be
// able to tell "forbidden" from "empty result" without inspecting business data.
func TestSecurityWorkOrderDenialIsNotMistakenForEmptyResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/security-work-orders/x", nil)
	writeWorkOrderResult(context, logger, "test", http.StatusOK, securityworkorder.WorkOrder{}, securityworkorder.ErrForbidden)
	if body := recorder.Body.String(); containsAll(body, `"item"`, `"total"`) {
		t.Errorf("denied work-order response carries a success payload: %s", body)
	}
	if !containsAll(recorder.Body.String(), `"error"`, `"forbidden"`) {
		t.Errorf("denied work-order response does not carry an explicit error envelope: %s", recorder.Body.String())
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		found := false
		for i := 0; i+len(needle) <= len(value); i++ {
			if value[i:i+len(needle)] == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
