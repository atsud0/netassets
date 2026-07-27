package pagination

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeUsesBoundedDefaults(t *testing.T) {
	query, err := Normalize(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if query.Page != DefaultPage || query.PageSize != DefaultPageSize || query.Offset() != 0 {
		t.Fatalf("Normalize(0, 0) = %+v, want page=%d page_size=%d offset=0", query, DefaultPage, DefaultPageSize)
	}
}

func TestNormalizeAcceptsMaximumPageSizeAndCalculatesOffset(t *testing.T) {
	query, err := Normalize(3, MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if query.Offset() != 200 {
		t.Fatalf("offset = %d, want 200", query.Offset())
	}
}

func TestNormalizeRejectsInvalidBounds(t *testing.T) {
	for _, input := range []struct {
		page     int
		pageSize int
	}{{-1, 20}, {1, -1}, {1, MaxPageSize + 1}} {
		if _, err := Normalize(input.page, input.pageSize); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Normalize(%d, %d) error = %v, want ErrInvalid", input.page, input.pageSize, err)
		}
	}
}

func TestNewResultSerializesNilItemsAsEmptyArray(t *testing.T) {
	result := NewResult[string](nil, 42, Query{Page: 2, PageSize: 20})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"items":[],"total":42,"page":2,"page_size":20}`
	if string(data) != expected {
		t.Fatalf("result JSON = %s, want %s", data, expected)
	}
}

func TestNeedsTotalFallback(t *testing.T) {
	if NeedsTotalFallback(0, Query{Page: 1, PageSize: 20}) {
		t.Fatal("empty first page must not require a fallback count")
	}
	if !NeedsTotalFallback(0, Query{Page: 2, PageSize: 20}) {
		t.Fatal("empty later page must require a fallback count")
	}
	if NeedsTotalFallback(1, Query{Page: 2, PageSize: 20}) {
		t.Fatal("non-empty later page already carries the window total")
	}
}
