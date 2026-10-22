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
		// Filter problems now report *FieldError, which unwraps to ErrFilterField
		// rather than ErrInvalid; only the name/page problems remain ErrInvalid.
		if _, err := NormalizeInput(input); !errors.Is(err, ErrFilterField) && !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d error = %v, want a validation error", index, err)
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

// The generic "请检查页面、名称和筛选条件" message could not be acted on: a user who
// typed a long search keyword and saw an empty list had no way to connect the refusal
// to their input. Each refusal must now name the field and say what is wrong with it.
func TestNormalizeInputNamesTheOffendingFilter(t *testing.T) {
	cases := []struct {
		name         string
		input        Input
		wantField    string
		wantContains []string
	}{
		{
			name:         "over-long keyword",
			input:        Input{PageKey: "assets", Name: "视图", Filters: map[string]string{"q": strings.Repeat("a", 201)}},
			wantField:    "q",
			wantContains: []string{"搜索关键词", "最多 200 个字", "201"},
		},
		{
			name:         "unsupported filter key",
			input:        Input{PageKey: "assets", Name: "视图", Filters: map[string]string{"foo": "bar"}},
			wantField:    "foo",
			wantContains: []string{"foo", "不被当前页面支持"},
		},
		{
			name:         "unsupported enum value",
			input:        Input{PageKey: "assets", Name: "视图", Filters: map[string]string{"type": "host"}},
			wantField:    "type",
			wantContains: []string{"资产类型", "取值不被支持"},
		},
		{
			name:         "malformed organization id",
			input:        Input{PageKey: "assets", Name: "视图", Filters: map[string]string{"organization_id": "not-a-uuid"}},
			wantField:    "organization_id",
			wantContains: []string{"所属组织", "取值不正确"},
		},
		{
			name:         "me is allowed for assignee",
			input:        Input{PageKey: "data_quality_issues", Name: "视图", Filters: map[string]string{"assignee": "me"}},
			wantField:    "",
			wantContains: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeInput(tc.input)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("NormalizeInput() error = %v, want nil", err)
				}
				return
			}
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("NormalizeInput() error = %v, want *FieldError", err)
			}
			if fieldErr.Field != tc.wantField {
				t.Fatalf("error field = %q, want %q", fieldErr.Field, tc.wantField)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(fieldErr.Reason, want) {
					t.Fatalf("error reason %q does not mention %q", fieldErr.Reason, want)
				}
			}
			// Callers switch on the sentinel, so the specific error must still satisfy
			// errors.Is(err, ErrFilterField) and must not be mistaken for ErrInvalid.
			if !errors.Is(err, ErrFilterField) {
				t.Fatal("error does not match ErrFilterField")
			}
			if errors.Is(err, ErrInvalid) {
				t.Fatal("a named filter error must not also report as ErrInvalid")
			}
		})
	}
}

// Go map iteration order is randomised, so a request with several bad filters used to
// produce a different message on each attempt. The reported field has to be stable.
func TestNormalizeInputReportsTheSameFieldEveryTime(t *testing.T) {
	input := Input{PageKey: "assets", Name: "视图", Filters: map[string]string{
		"zzz_unknown": "x",
		"type":        "host",
		"q":           strings.Repeat("a", 500),
		"status":      "gone",
	}}
	first := ""
	for attempt := 0; attempt < 50; attempt++ {
		_, err := NormalizeInput(input)
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) {
			t.Fatalf("attempt %d error = %v, want *FieldError", attempt, err)
		}
		if first == "" {
			first = fieldErr.Field
			continue
		}
		if fieldErr.Field != first {
			t.Fatalf("attempt %d reported %q but the first attempt reported %q", attempt, fieldErr.Field, first)
		}
	}
	if first != "q" {
		t.Fatalf("reported field = %q, want q (alphabetically first of the bad keys)", first)
	}
}

// The over-long keyword case is the one users hit: the search box had no limit, so a
// long phrase was accepted, matched nothing, and could then not be saved as a view.
func TestKeywordBoundaryIsExactlyTwoHundredCharacters(t *testing.T) {
	for _, tc := range []struct {
		length  int
		wantErr bool
	}{{200, false}, {201, true}} {
		_, err := NormalizeInput(Input{PageKey: "assets", Name: "视图", Filters: map[string]string{"q": strings.Repeat("字", tc.length)}})
		if tc.wantErr != (err != nil) {
			t.Fatalf("q of %d characters: error = %v, want error = %v", tc.length, err, tc.wantErr)
		}
	}
}

func TestFieldLabelFallsBackToTheKey(t *testing.T) {
	if got := FieldLabel("assets", "type"); got != "资产类型" {
		t.Fatalf("FieldLabel(assets, type) = %q, want 资产类型", got)
	}
	if got := FieldLabel("assets", "mystery"); got != "mystery" {
		t.Fatalf("FieldLabel fallback = %q, want the raw key", got)
	}
}
