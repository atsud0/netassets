package securityworkorder

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIndividualWorkOrderLegacyActionsAreBlocked(t *testing.T) {
	for _, action := range []string{"accept", "progress", "submit", "return", "close"} {
		if !individualWorkOrderActionForbidden(action) {
			t.Fatalf("individual action %q should be blocked", action)
		}
	}
	for _, action := range []string{"cancel", "issue", "create"} {
		if individualWorkOrderActionForbidden(action) {
			t.Fatalf("individual action %q should not be classified as a legacy execution action", action)
		}
	}
}

func TestIndividualWorkOrderErrorPreservesConflictSemantics(t *testing.T) {
	err := fmt.Errorf("%w: %w", ErrConflict, ErrIndividualWorkOrderAction)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("individual work-order error must remain a conflict")
	}
	if !errors.Is(err, ErrIndividualWorkOrderAction) {
		t.Fatal("individual work-order error must expose its dedicated sentinel")
	}
}

func TestNormalizeFindingInputDefaults(t *testing.T) {
	input := FindingInput{
		Title:                  "  未授权访问  ",
		Severity:               "high",
		AssigneeOrganizationID: "11111111-1111-1111-1111-111111111111",
		AssetID:                "22222222-2222-2222-2222-222222222222",
		Description:            "  漏洞描述  ",
	}
	normalizeFindingInput(&input)
	if input.Title != "未授权访问" || input.Description != "漏洞描述" {
		t.Fatalf("finding text was not normalized: %+v", input)
	}
	if input.Status != "pending" || input.Source != "manual" || input.DiscoveredAt.IsZero() {
		t.Fatalf("finding defaults were not applied: %+v", input)
	}
	if !validFindingInput(input) {
		t.Fatal("normalized finding should be valid")
	}
}

func TestFindingRequiresAssetOrBusinessSystem(t *testing.T) {
	input := FindingInput{
		Title: "漏洞", Severity: "high", Status: "pending",
		AssigneeOrganizationID: "11111111-1111-1111-1111-111111111111",
		Description:            "描述", Source: "manual", DiscoveredAt: time.Now(),
	}
	if validFindingInput(input) {
		t.Fatal("finding without asset and business system should be invalid")
	}
}

func TestNormalizeWorkOrderInputAndSeverity(t *testing.T) {
	input := WorkOrderInput{
		Title: "  整改工单  ", AssigneeOrganizationID: "11111111-1111-1111-1111-111111111111",
		RemediationRequirement: "  完成修复  ", DueAt: time.Now().Add(24 * time.Hour),
		FindingIDs: []string{"22222222-2222-2222-2222-222222222222", "22222222-2222-2222-2222-222222222222", " "},
	}
	normalizeWorkOrderInput(&input)
	if input.Title != "整改工单" || input.RemediationRequirement != "完成修复" || len(input.FindingIDs) != 1 {
		t.Fatalf("work order was not normalized: %+v", input)
	}
	if !validWorkOrderInput(input) {
		t.Fatal("normalized work order should be valid")
	}
	if severityRank("critical") <= severityRank("high") || severityRank("low") <= severityRank("info") {
		t.Fatal("severity ranking is incorrect")
	}
}

func TestNewCode(t *testing.T) {
	first, err := newCode("WO")
	if err != nil {
		t.Fatalf("newCode() error = %v", err)
	}
	second, err := newCode("WO")
	if err != nil {
		t.Fatalf("newCode() error = %v", err)
	}
	if first == second || !strings.HasPrefix(first, "WO-"+time.Now().Format("20060102")+"-") {
		t.Fatalf("unexpected generated codes: %q, %q", first, second)
	}
}

func TestAttachmentFilenameAndCategoryValidation(t *testing.T) {
	if got := sanitizeOriginalFilename(`../evidence/report.pdf`); got != "report.pdf" {
		t.Fatalf("sanitizeOriginalFilename() = %q, want report.pdf", got)
	}
	if got := sanitizeOriginalFilename(`..\\evidence\\proof.txt`); got != "proof.txt" {
		t.Fatalf("sanitizeOriginalFilename(windows) = %q, want proof.txt", got)
	}
	if !validAttachmentCategory("remediation") || !validAttachmentCategory("verification") || validAttachmentCategory("unknown") {
		t.Fatal("attachment category validation is incorrect")
	}
	if _, ok := allowedAttachmentExtensions[".exe"]; ok {
		t.Fatal("executable attachment extension must not be allowed")
	}
}

func TestFindingImportSeverityAndHeaders(t *testing.T) {
	for input, want := range map[string]string{
		"严重": "critical", "高危": "high", "中": "medium", "低风险": "low", "提示": "info", "HIGH": "high",
	} {
		if got := normalizeFindingSeverity(input); got != want {
			t.Fatalf("normalizeFindingSeverity(%q) = %q, want %q", input, got, want)
		}
	}
	headers := mapFindingImportHeaders([]string{"\ufeff资产类型", "资产值", "漏洞标题", "严重等级", "漏洞描述"})
	for _, key := range []string{"asset_type", "asset_value", "title", "severity", "description"} {
		if headers[key] < 0 {
			t.Fatalf("required header %q was not mapped: %#v", key, headers)
		}
	}
}

func TestReadFindingImportCSVWithBOM(t *testing.T) {
	content := "\xEF\xBB\xBF资产类型,资产值,漏洞标题,严重等级,漏洞描述\nurl,https://example.com,测试漏洞,高危,漏洞说明\n"
	rows, fileType, err := readFindingImportRows("findings.csv", strings.NewReader(content))
	if err != nil {
		t.Fatalf("readFindingImportRows() error = %v", err)
	}
	if fileType != "csv" || len(rows) != 2 {
		t.Fatalf("unexpected CSV result: type=%q rows=%d", fileType, len(rows))
	}
	headers := mapFindingImportHeaders(rows[0])
	input, assetType, assetValue, rowErr := findingInputFromImportRow(rows[1], headers, "11111111-1111-1111-1111-111111111111")
	if rowErr != nil || assetType != "url" || assetValue != "https://example.com" || input.Severity != "high" || input.Source != "import" {
		t.Fatalf("unexpected parsed finding: input=%+v type=%q value=%q err=%+v", input, assetType, assetValue, rowErr)
	}
}

func TestFindingImportXLSXTemplateCanBeRead(t *testing.T) {
	data, contentType, fileName, err := FindingImportTemplate("xlsx")
	if err != nil {
		t.Fatalf("FindingImportTemplate() error = %v", err)
	}
	if contentType == "" || fileName != "漏洞导入模板.xlsx" {
		t.Fatalf("unexpected template metadata: %q %q", contentType, fileName)
	}
	rows, fileType, err := readFindingImportRows(fileName, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("read xlsx template: %v", err)
	}
	if fileType != "xlsx" || len(rows) < 2 || mapFindingImportHeaders(rows[0])["asset_value"] < 0 {
		t.Fatalf("unexpected XLSX template rows: type=%q rows=%d", fileType, len(rows))
	}
}

func TestParseFindingImportTime(t *testing.T) {
	parsed, err := parseFindingImportTime("2026-07-22 10:00:00")
	if err != nil || parsed.Year() != 2026 || parsed.Month() != time.July || parsed.Day() != 22 {
		t.Fatalf("parseFindingImportTime() = %v, %v", parsed, err)
	}
	if _, err := parseFindingImportTime("not-a-date"); err == nil {
		t.Fatal("invalid import time should return an error")
	}
}

func TestRiskAcceptanceValidation(t *testing.T) {
	if validRiskAcceptanceInput(RiskAcceptanceInput{Reason: "", AcceptedUntil: time.Now().Add(time.Hour)}) {
		t.Fatal("risk acceptance should require a reason")
	}
	if validRiskAcceptanceInput(RiskAcceptanceInput{Reason: "业务原因", AcceptedUntil: time.Now().Add(-time.Hour)}) {
		t.Fatal("risk acceptance should require a future expiration")
	}
	if !validRiskAcceptanceInput(RiskAcceptanceInput{Reason: "  业务原因  ", AcceptedUntil: time.Now().Add(time.Hour)}) {
		t.Fatal("future risk acceptance should be valid")
	}
}

// A finding can be linked to a live asset, to a working version that has not been
// published yet, to an asset that has since been retired, or to nothing at all.
// The four cases look identical in the API unless the server says which one it is:
// an empty asset cell on a retired asset reads exactly like a finding that was
// never linked, and reviewers cannot tell whether the data is broken or expected.
func TestFindingAssetLinkState(t *testing.T) {
	const (
		draftID = "11111111-1111-1111-1111-111111111111"
		assetID = "22222222-2222-2222-2222-222222222222"
	)
	cases := []struct {
		name           string
		assetID        string
		pendingDraftID string
		assetDeleted   bool
		want           string
	}{
		{name: "live asset", assetID: assetID, want: "live"},
		{name: "asset soft deleted", assetID: assetID, assetDeleted: true, want: "retired"},
		{name: "waiting on unpublished working version", pendingDraftID: draftID, want: "pending"},
		{name: "never linked", want: "unlinked"},
		// Publishing clears the draft in the same transaction, so a row carrying
		// both means the asset is live and the draft column is stale. The live
		// asset is the actionable fact.
		{name: "asset set means live even if draft column is stale", assetID: assetID, pendingDraftID: draftID, want: "live"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findingAssetLinkState(tc.assetID, tc.pendingDraftID, tc.assetDeleted); got != tc.want {
				t.Fatalf("findingAssetLinkState() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The filter must cover exactly the four states the vocabulary emits, otherwise
// the dashboard shortcut silently shows the wrong rows.
func TestValidFindingAssetLinkState(t *testing.T) {
	for _, state := range []string{"live", "pending", "retired", "unlinked"} {
		if !validFindingAssetLinkState(state) {
			t.Fatalf("validFindingAssetLinkState(%q) = false, want true", state)
		}
	}
	for _, state := range []string{"", "LIVE", "retired_asset", "deleted", "unknown"} {
		if validFindingAssetLinkState(state) {
			t.Fatalf("validFindingAssetLinkState(%q) = true, want false", state)
		}
	}
}

func TestFindingAssetLinkStateFilterCoversEveryState(t *testing.T) {
	fragment := findingAssetLinkStateFilter("f", "a", "$5")
	for _, state := range []string{"live", "pending", "retired", "unlinked"} {
		if !strings.Contains(fragment, "'"+state+"'") {
			t.Fatalf("filter fragment missing %q branch:\n%s", state, fragment)
		}
	}
	// The empty placeholder branch must short-circuit so an unfiltered search
	// keeps returning every row.
	if !strings.Contains(fragment, "= ''") {
		t.Fatalf("filter fragment missing the no-op empty value branch:\n%s", fragment)
	}
}
