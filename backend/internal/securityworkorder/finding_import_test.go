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
