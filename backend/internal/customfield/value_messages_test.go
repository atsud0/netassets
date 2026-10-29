package customfield

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCustomValueFieldMessages(t *testing.T) {
	definitions := []Definition{{FieldKey: "retention", FieldName: "保留天数", FieldType: "number", IsRequired: true}}
	for _, tc := range []struct {
		name    string
		values  []Value
		message string
	}{
		{"unknown", []Value{{FieldKey: "unknown", Value: "secret"}}, "自定义字段编码无效，请刷新后重试"},
		{"type", []Value{{FieldKey: "retention", Value: "invalid"}}, "自定义字段“保留天数”：须为数字"},
		{"duplicate", []Value{{FieldKey: "retention", Value: 3.0}, {FieldKey: "retention", Value: 4.0}}, "自定义字段“保留天数”不能重复填写"},
		{"required", nil, "自定义字段“保留天数”不能为空"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateValues(definitions, tc.values)
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}

func TestCustomValueMessagesPreserveAcceptedValues(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		value      any
		options    []string
	}{
		{"text", "text", strings.Repeat("文😀", 128), nil},
		{"textarea", "textarea", "文本\n正文", nil},
		{"date", "date", "2026-10-29", nil},
		{"number zero", "number", float64(0), nil},
		{"number fractional", "number", float64(1.5), nil},
		{"boolean false", "boolean", false, nil},
		{"boolean true", "boolean", true, nil},
		{"select", "select", "选项一", []string{"选项一", "选项二"}},
		{"multiselect", "multiselect", []any{"选项一", "选项二"}, []string{"选项一", "选项二"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := Definition{FieldKey: "field", FieldName: "公开字段标题", FieldType: tc.kind, Options: tc.options, IsRequired: true}
			byKey, values, err := ValidateValues([]Definition{definition}, []Value{{FieldKey: "field", Value: tc.value}})
			if err != nil || len(byKey) != 1 || !reflect.DeepEqual(values["field"], tc.value) {
				t.Fatalf("valid value altered or rejected: %v", err)
			}
		})
	}
	for _, kind := range []string{"text", "textarea", "date", "number", "boolean", "select", "multiselect"} {
		t.Run("optional blank/"+kind, func(t *testing.T) {
			definitions := []Definition{{FieldKey: "optional", FieldType: kind}}
			for _, values := range [][]Value{nil, {{FieldKey: "optional", Value: nil}}, {{FieldKey: "optional", Value: " "}}} {
				if _, _, err := ValidateValues(definitions, values); err != nil {
					t.Fatalf("optional blank rejected: %v", err)
				}
			}
		})
	}
}

func TestCustomValueMessagesDoNotEchoUnknownKeysOrValues(t *testing.T) {
	private := "PRIVATE_INPUT_MUST_NOT_BE_PUBLIC"
	for _, tc := range []struct {
		definition Definition
		values     []Value
	}{
		{Definition{FieldKey: "field", FieldName: "公开标题", FieldType: "number"}, []Value{{FieldKey: "field", Value: private}}},
		{Definition{FieldKey: "field", FieldName: "公开标题", FieldType: "number"}, []Value{{FieldKey: private, Value: private}}},
		{Definition{FieldKey: "field", FieldType: "number"}, []Value{{FieldKey: "field", Value: private}}},
	} {
		_, _, err := ValidateValues([]Definition{tc.definition}, tc.values)
		var typed *ValueValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || strings.Contains(err.Error(), private) {
			t.Fatalf("unsafe or untyped validation: %v", err)
		}
	}
}
