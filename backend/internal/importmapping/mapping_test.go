package importmapping

import "testing"

func testFields() []Field {
	return []Field{
		{Key: "type", Name: "资产类型", Required: true, Aliases: []string{"资产类型"}},
		{Key: "value", Name: "资产值", Required: true, Aliases: []string{"资产值"}},
		{Key: "custom:security_level", Name: "安全等级", Required: true, Custom: true, Aliases: []string{"custom:security_level", "安全等级"}},
	}
}

func TestAnalyzeSuggestsColumnsAndExamples(t *testing.T) {
	analysis, err := Analyze([][]string{{"主机类别", "目标", "安全等级"}, {"IP", "1.2.3.4", "高"}, {"域名", "example.com", "中"}}, []Field{
		{Key: "type", Name: "资产类型", Required: true, Aliases: []string{"主机类别"}},
		{Key: "value", Name: "资产值", Required: true, Aliases: []string{"目标"}},
		{Key: "custom:security_level", Name: "安全等级", Custom: true, Aliases: []string{"安全等级"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Columns) != 3 || len(analysis.Columns[1].Examples) != 2 || analysis.Columns[1].Examples[0] != "1.2.3.4" {
		t.Fatalf("unexpected columns: %#v", analysis.Columns)
	}
	for index, field := range analysis.Fields {
		if field.SuggestedColumn == nil || *field.SuggestedColumn != index {
			t.Fatalf("unexpected suggestion for %s: %#v", field.Key, field.SuggestedColumn)
		}
	}
}

func TestResolveExplicitMappingNormalizesCustomHeader(t *testing.T) {
	indexes, headers, err := Resolve([]string{"主机类别", "目标地址", "分级"}, Mapping{
		"type": 0, "value": 1, "custom:security_level": 2,
	}, testFields())
	if err != nil {
		t.Fatal(err)
	}
	if indexes["value"] != 1 || headers[2] != "custom:security_level" {
		t.Fatalf("unexpected resolved mapping: %#v %#v", indexes, headers)
	}
}

func TestResolveRejectsInvalidMappings(t *testing.T) {
	tests := []Mapping{
		{"type": 0, "custom:security_level": 2},
		{"type": 0, "value": 0, "custom:security_level": 2},
		{"type": 0, "value": 1, "custom:security_level": 9},
		{"type": 0, "value": 1, "custom:unknown": 2},
	}
	for _, mapping := range tests {
		if _, _, err := Resolve([]string{"a", "b", "c"}, mapping, testFields()); err != ErrInvalid {
			t.Fatalf("Resolve(%#v) error = %v", mapping, err)
		}
	}
}

func TestResolveEmptyMappingKeepsStandardTemplateCompatibility(t *testing.T) {
	indexes, headers, err := Resolve([]string{"资产类型", "资产值", "custom:security_level"}, nil, testFields())
	if err != nil {
		t.Fatal(err)
	}
	if indexes["type"] != 0 || indexes["value"] != 1 || indexes["custom:security_level"] != 2 || headers[2] != "custom:security_level" {
		t.Fatalf("unexpected automatic mapping: %#v %#v", indexes, headers)
	}
}

func TestResolveAutomaticMappingPreservesUnknownCustomHeadersForExistingValidation(t *testing.T) {
	_, headers, err := Resolve([]string{"资产类型", "资产值", "custom:unknown", "custom:security_level"}, nil, testFields())
	if err != nil {
		t.Fatal(err)
	}
	if headers[2] != "custom:unknown" || headers[3] != "custom:security_level" {
		t.Fatalf("unexpected normalized headers: %#v", headers)
	}
}
