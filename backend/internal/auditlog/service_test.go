package auditlog

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeFilterDefaultsAndValidation(t *testing.T) {
	filter, err := NormalizeFilter(Filter{Keyword: "  资产操作  "})
	if err != nil {
		t.Fatalf("NormalizeFilter() error = %v", err)
	}
	if filter.Page != DefaultPage || filter.PageSize != DefaultPageSize {
		t.Fatalf("page = %d, page size = %d", filter.Page, filter.PageSize)
	}
	if filter.Keyword != "资产操作" {
		t.Fatalf("keyword = %q", filter.Keyword)
	}

	invalidCases := []Filter{
		{Page: 1, PageSize: MaxPageSize + 1},
		{Page: 1, PageSize: 20, Result: "unknown"},
		{Page: 1, PageSize: 20, UserID: "not-a-uuid"},
		{Page: 1, PageSize: 20, OrganizationID: "12345678-1234-1234-1234-nothexvalue1"},
	}
	for _, input := range invalidCases {
		if _, err := NormalizeFilter(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("NormalizeFilter(%+v) error = %v, want ErrInvalid", input, err)
		}
	}
}

func TestNormalizeFilterRejectsInvalidTimeRange(t *testing.T) {
	start := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	end := start
	_, err := NormalizeFilter(Filter{Page: 1, PageSize: 20, StartAt: &start, EndAt: &end})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestParseTimeDateUsesShanghaiAndExclusiveEnd(t *testing.T) {
	start, err := ParseTime("2026-07-22", false)
	if err != nil {
		t.Fatalf("ParseTime(start) error = %v", err)
	}
	end, err := ParseTime("2026-07-22", true)
	if err != nil {
		t.Fatalf("ParseTime(end) error = %v", err)
	}
	if start.Format(time.RFC3339) != "2026-07-22T00:00:00+08:00" {
		t.Fatalf("start = %s", start.Format(time.RFC3339))
	}
	if end.Format(time.RFC3339) != "2026-07-23T00:00:00+08:00" {
		t.Fatalf("end = %s", end.Format(time.RFC3339))
	}
}

func TestAuditLabels(t *testing.T) {
	if got := ActionLabel("inventory_task.review"); got != "审核填报" {
		t.Fatalf("ActionLabel = %q", got)
	}
	if got := ResourceTypeLabel("business_system"); got != "业务系统" {
		t.Fatalf("ResourceTypeLabel = %q", got)
	}
	if got := ResultLabel("failure"); got != "失败" {
		t.Fatalf("ResultLabel = %q", got)
	}
	if got := ActionLabel("plugin.custom"); got != "plugin.custom" {
		t.Fatalf("unknown ActionLabel = %q", got)
	}
}
