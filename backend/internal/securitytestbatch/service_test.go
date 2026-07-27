package securitytestbatch

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeAndValidateInput(t *testing.T) {
	input := Input{
		Name: "  2026年第三季度互联网资产渗透测试  ", TestType: "penetration",
		Provider: "  集团安全团队  ", AuthorizationNote: "  经授权仅测试所列目标  ",
		StartAt: time.Now().Add(time.Hour), EndAt: time.Now().Add(24 * time.Hour),
		AssetIDs: []string{"11111111-1111-1111-1111-111111111111", "11111111-1111-1111-1111-111111111111"},
	}
	normalizeInput(&input)
	if input.Name != "2026年第三季度互联网资产渗透测试" || input.Provider != "集团安全团队" || len(input.AssetIDs) != 1 {
		t.Fatalf("input was not normalized: %+v", input)
	}
	if !validInput(input) {
		t.Fatal("normalized input should be valid")
	}
}

func TestInvalidScopeAndTime(t *testing.T) {
	base := Input{Name: "批次", TestType: "scan", Provider: "安全团队", AuthorizationNote: "授权说明",
		StartAt: time.Now().Add(2 * time.Hour), EndAt: time.Now().Add(time.Hour),
		AssetIDs: []string{"11111111-1111-1111-1111-111111111111"}}
	if validInput(base) {
		t.Fatal("end time before start time must be invalid")
	}
	base.EndAt = time.Now().Add(3 * time.Hour)
	base.AssetIDs = nil
	if validInput(base) {
		t.Fatal("empty scope must be invalid")
	}
}

func TestBatchCodeAndStates(t *testing.T) {
	first, err := newCode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCode()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "ST-"+time.Now().Format("20060102")+"-") {
		t.Fatalf("unexpected codes %q %q", first, second)
	}
	if !validStatus("authorized") || validStatus("closed") || !validTestType("penetration") || validTestType("unknown") {
		t.Fatal("status or test type validation is incorrect")
	}
}

func TestAttachmentFilenameAndCategoryValidation(t *testing.T) {
	if got := sanitizeOriginalFilename(`../reports/final-report.pdf`); got != "final-report.pdf" {
		t.Fatalf("sanitizeOriginalFilename() = %q, want final-report.pdf", got)
	}
	if got := sanitizeOriginalFilename(`..\\reports\\scan-result.txt`); got != "scan-result.txt" {
		t.Fatalf("sanitizeOriginalFilename(windows) = %q, want scan-result.txt", got)
	}
	if !validAttachmentCategory("test_report") || !validAttachmentCategory("reference") || validAttachmentCategory("remediation") {
		t.Fatal("security test batch attachment category validation is incorrect")
	}
	if _, ok := allowedAttachmentExtensions[".exe"]; ok {
		t.Fatal("executable attachment extension must not be allowed")
	}
	if got := normalizeContentType(" text/plain; charset=utf-8 "); got != "text/plain" {
		t.Fatalf("normalizeContentType() = %q, want text/plain", got)
	}
}
