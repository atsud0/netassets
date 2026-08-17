package httpserver

import "testing"

func TestParseImportColumnMapping(t *testing.T) {
	mapping, err := parseImportColumnMapping(`{"type":0,"value":2}`)
	if err != nil || mapping["type"] != 0 || mapping["value"] != 2 {
		t.Fatalf("unexpected mapping: %#v err=%v", mapping, err)
	}
	if _, err := parseImportColumnMapping(`{"type":"0"}`); err == nil {
		t.Fatal("expected invalid JSON value type to fail")
	}
	mapping, err = parseImportColumnMapping("")
	if err != nil || mapping != nil {
		t.Fatalf("empty mapping = %#v err=%v", mapping, err)
	}
}
