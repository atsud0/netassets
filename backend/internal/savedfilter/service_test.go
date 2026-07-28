package savedfilter

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeInput(t *testing.T) {
	input, err := NormalizeInput(Input{
		PageKey: " assets ", Name: " 公网待确认 ",
		Filters:   map[string]string{"q": " example.com ", "status": "pending", "organization_id": ""},
		IsDefault: true,
	})
	if err != nil {
		t.Fatalf("NormalizeInput() error = %v", err)
	}
	if input.PageKey != "assets" || input.Name != "公网待确认" || !input.IsDefault {
		t.Fatalf("unexpected normalized input: %#v", input)
	}
	want := map[string]string{"q": "example.com", "status": "pending"}
	if !reflect.DeepEqual(input.Filters, want) {
		t.Fatalf("filters = %#v, want %#v", input.Filters, want)
	}
}

func TestNormalizeInputAcceptsSupportedPages(t *testing.T) {
	cases := []Input{
		{PageKey: "assets", Name: "资产", Filters: map[string]string{"type": "url", "status": "active"}},
		{PageKey: "security_work_orders", Name: "逾期工单", Filters: map[string]string{"status": "issued", "overdue": "true"}},
		{PageKey: "data_quality_issues", Name: "我的错误问题", Filters: map[string]string{"severity": "error", "assignee": "me"}},
	}
	for _, input := range cases {
		if _, err := NormalizeInput(input); err != nil {
			t.Errorf("NormalizeInput(%s) error = %v", input.PageKey, err)
		}
	}
}

func TestNormalizeInputRejectsUnsafeFilters(t *testing.T) {
	validUUID := "2cf486e9-71c8-4a48-82e4-17e3ab76b53d"
	cases := []Input{
		{PageKey: "unknown", Name: "x"},
		{PageKey: "assets", Name: "", Filters: map[string]string{}},
		{PageKey: "assets", Name: strings.Repeat("字", 101), Filters: map[string]string{}},
		{PageKey: "assets", Name: "x", Filters: map[string]string{"sql": "drop table assets"}},
		{PageKey: "assets", Name: "x", Filters: map[string]string{"type": "host"}},
		{PageKey: "assets", Name: "x", Filters: map[string]string{"q": strings.Repeat("a", 201)}},
		{PageKey: "assets", Name: "x", Filters: map[string]string{"organization_id": "not-a-uuid"}},
		{PageKey: "data_quality_issues", Name: "x", Filters: map[string]string{"assignee": "somebody"}},
	}
	for index, input := range cases {
		if _, err := NormalizeInput(input); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d error = %v, want ErrInvalid", index, err)
		}
	}
	if _, err := NormalizeInput(Input{PageKey: "assets", Name: "x", Filters: map[string]string{"organization_id": validUUID}}); err != nil {
		t.Fatalf("valid UUID rejected: %v", err)
	}
}

func TestValidPageKey(t *testing.T) {
	for _, key := range []string{"assets", "security_work_orders", "data_quality_issues"} {
		if !ValidPageKey(key) {
			t.Errorf("ValidPageKey(%q) = false", key)
		}
	}
	if ValidPageKey("users") {
		t.Fatal("users must not be an allowed saved-view page")
	}
}
