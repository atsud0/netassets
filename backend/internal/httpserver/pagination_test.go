package httpserver

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func paginationTestContext(rawQuery string) *gin.Context {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return context
}

func TestParsePageQueryUsesDefaults(t *testing.T) {
	query, err := parsePageQuery(paginationTestContext(""))
	if err != nil {
		t.Fatal(err)
	}
	if query.Page != 1 || query.PageSize != 20 {
		t.Fatalf("query = %+v, want page=1 page_size=20", query)
	}
}

func TestParsePageQueryUsesRequestedPage(t *testing.T) {
	query, err := parsePageQuery(paginationTestContext("page=3&page_size=50"))
	if err != nil {
		t.Fatal(err)
	}
	if query.Page != 3 || query.PageSize != 50 || query.Offset() != 100 {
		t.Fatalf("query = %+v, want page=3 page_size=50 offset=100", query)
	}
}

func TestParsePageQueryRejectsMalformedOrUnboundedValues(t *testing.T) {
	for _, rawQuery := range []string{"page=abc", "page=0", "page_size=-1", "page_size=101"} {
		if _, err := parsePageQuery(paginationTestContext(rawQuery)); err == nil {
			t.Fatalf("parsePageQuery(%q) error = nil, want error", rawQuery)
		}
	}
}
