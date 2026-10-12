package securityworkorder

import "testing"

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
