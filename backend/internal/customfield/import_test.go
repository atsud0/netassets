package customfield

import (
	"strings"
	"testing"
)

func TestImportValuesNormalizesSupportedTypes(t *testing.T) {
	definitions := []Definition{
		{FieldKey: "retention", FieldName: "数据保留期限", FieldType: "number", IsRequired: true},
		{FieldKey: "classified", FieldName: "是否完成定级", FieldType: "boolean"},
		{FieldKey: "regions", FieldName: "部署区域", FieldType: "multiselect", Options: []string{"华东", "华南"}},
		{FieldKey: "go_live", FieldName: "上线日期", FieldType: "date"},
	}
	values, err := ImportValues(definitions,
		[]string{"资产值", "custom:retention", "custom:classified", "custom:regions", "custom:go_live"},
		[]string{"https://portal.example.com", "100", "是", "华东；华南；华东", "2026-01-01"},
	)
	if err != nil {
		t.Fatalf("ImportValues() error = %v", err)
	}
	if len(values) != 4 {
		t.Fatalf("value count = %d, want 4", len(values))
	}
	if got, ok := values[0].Value.(float64); !ok || got != 100 {
		t.Fatalf("number value = %#v, want float64(100)", values[0].Value)
	}
	if got, ok := values[1].Value.(bool); !ok || !got {
		t.Fatalf("boolean value = %#v, want true", values[1].Value)
	}
	regions, ok := values[2].Value.([]any)
	if !ok || len(regions) != 2 || regions[0] != "华东" || regions[1] != "华南" {
		t.Fatalf("multiselect value = %#v, want [华东 华南]", values[2].Value)
	}
}

func TestImportValuesRejectsUnknownAndMissingRequiredFields(t *testing.T) {
	definitions := []Definition{{FieldKey: "data_level", FieldName: "数据等级", FieldType: "select", Options: []string{"一般", "重要"}, IsRequired: true}}
	_, err := ImportValues(definitions, []string{"custom:data_level"}, []string{""})
	if err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("missing required error = %v", err)
	}
	_, err = ImportValues(definitions, []string{"custom:removed_field"}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "未知或已停用") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestExportValueUsesSpreadsheetFriendlyForms(t *testing.T) {
	if got := ExportValue(Definition{FieldType: "boolean"}, true); got != "是" {
		t.Fatalf("boolean export = %q", got)
	}
	if got := ExportValue(Definition{FieldType: "multiselect"}, []any{"华东", "华南"}); got != "华东；华南" {
		t.Fatalf("multiselect export = %q", got)
	}
	if got := ExportValue(Definition{FieldType: "number"}, float64(1.5)); got != "1.5" {
		t.Fatalf("number export = %q", got)
	}
}
