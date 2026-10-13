package securityworkorder

import (
	"strings"
	"testing"
)

func TestReadFindingImportJSONRepairsInvalidReportEscapes(t *testing.T) {
	data := []byte(`[{"name":"test finding","severity":"low","summary":"report mentions \c.target","assets":[{"type":"domain","label":"example.com"}]}]`)

	rows, fileType, err := readFindingImportJSON(data)
	if err != nil {
		t.Fatalf("readFindingImportJSON() error = %v", err)
	}
	if fileType != "json" {
		t.Fatalf("file type = %q, want json", fileType)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want header and one finding", len(rows))
	}
	if rows[1][2] != "test finding" || rows[1][5] != "report mentions c.target" {
		t.Fatalf("mapped row = %#v", rows[1])
	}
}

func TestRepairInvalidJSONEscapesPreservesValidEscapes(t *testing.T) {
	input := []byte(`{"text":"line\nquote: \"ok\" path: \\server invalid: \c.target bad unicode: \u12"}`)
	repaired := repairInvalidJSONEscapes(input)
	want := `{"text":"line\nquote: \"ok\" path: \\server invalid: c.target bad unicode: u12"}`
	if string(repaired) != want {
		t.Fatalf("repaired JSON = %s, want %s", repaired, want)
	}
}

func TestReadFindingImportJSONPreservesExportStatus(t *testing.T) {
	data := []byte(`[{"finding_id":"168","name":"已导出漏洞","severity":"low","status":"ignored","summary":"说明","assets":[{"type":"subdomain","label":"example.com"}]}]`)
	rows, _, err := readFindingImportJSON(data)
	if err != nil {
		t.Fatalf("readFindingImportJSON() error = %v", err)
	}
	headers := mapFindingImportHeaders(rows[0])
	if got := rows[1][headers["status"]]; got != "ignored" {
		t.Fatalf("imported status = %q, want ignored", got)
	}
	input, _, _, rowErr := findingInputFromImportRow(rows[1], headers, "")
	if rowErr != nil {
		t.Fatalf("findingInputFromImportRow() error = %#v", rowErr)
	}
	if input.Status != "ignored" {
		t.Fatalf("FindingInput.Status = %q, want ignored", input.Status)
	}
}

func TestFindingImportStatusVocabulary(t *testing.T) {
	for _, value := range []string{"pending", "in_progress", "confirmed", "resolved", "fixed", "false_positive", "ignored", "duplicate", "risk_accepted", "待处理", "已修复", "风险接受"} {
		if got := normalizeFindingImportStatus(value); got == "" {
			t.Fatalf("normalizeFindingImportStatus(%q) returned empty", value)
		}
	}
	if got := normalizeFindingImportStatus(""); got != "pending" {
		t.Fatalf("empty status = %q, want pending", got)
	}
	if got := normalizeFindingImportStatus("not-a-status"); got != "" {
		t.Fatalf("invalid status = %q, want empty", got)
	}
}

func TestReadFindingImportJSONRejectsInvalidExportStatus(t *testing.T) {
	data := []byte(`[{"name":"状态错误漏洞","severity":"low","status":"closed","summary":"说明","assets":[{"type":"subdomain","label":"example.com"}]}]`)
	rows, _, err := readFindingImportJSON(data)
	if err != nil {
		t.Fatalf("readFindingImportJSON() error = %v", err)
	}
	headers := mapFindingImportHeaders(rows[0])
	_, _, _, rowErr := findingInputFromImportRow(rows[1], headers, "")
	if rowErr == nil || rowErr.code != "invalid_status" {
		t.Fatalf("row error = %#v, want invalid_status", rowErr)
	}
}

func TestNormalizeFindingImportStatusEvidence(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		normalized string
		wantRaw    string
		wantReason string
	}{
		{name: "empty defaults pending", raw: "", normalized: "pending", wantReason: "未提供外部状态"},
		{name: "english is retained", raw: "confirmed", normalized: "confirmed", wantRaw: "confirmed", wantReason: "本地标准状态"},
		{name: "chinese maps", raw: "已修复", normalized: "fixed", wantRaw: "已修复", wantReason: "中文状态标签"},
		{name: "risk acceptance is conservative", raw: "risk_accepted", normalized: "confirmed", wantRaw: "risk_accepted", wantReason: "缺少本地风险接受"},
		{name: "duplicate is conservative", raw: "重复", normalized: "pending", wantRaw: "重复", wantReason: "缺少本地主漏洞"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFindingImportStatusEvidence(tc.raw)
			if err != nil {
				t.Fatalf("normalizeFindingImportStatusEvidence() error = %v", err)
			}
			if got.Raw != tc.wantRaw || got.Normalized != tc.normalized || !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("evidence = %#v, want raw=%q normalized=%q reason containing %q", got, tc.wantRaw, tc.normalized, tc.wantReason)
			}
		})
	}
	if _, err := normalizeFindingImportStatusEvidence("closed"); err == nil {
		t.Fatal("invalid external status should be rejected")
	}
}

func TestFindingInputFromImportRowPersistsStatusEvidence(t *testing.T) {
	headers := mapFindingImportHeaders([]string{"资产类型", "资产值", "漏洞标题", "严重等级", "漏洞描述", "状态"})
	row := []string{"ip", "192.0.2.10", "外部风险接受", "高危", "需要本地复核", "风险接受"}
	input, _, _, rowErr := findingInputFromImportRow(row, headers, "")
	if rowErr != nil {
		t.Fatalf("findingInputFromImportRow() error = %#v", rowErr)
	}
	if input.Status != "confirmed" || input.ImportStatusRaw != "风险接受" || input.ImportStatusNormalized != "confirmed" {
		t.Fatalf("input status evidence = %#v", input)
	}
	if input.ImportStatusMappingReason == "" {
		t.Fatal("status mapping reason should be retained")
	}
}
