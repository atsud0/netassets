package securitytestrequest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"netassets/internal/dataquality"
)

func validInputBase() Input {
	return Input{
		RequestName:       "客户服务平台二期上线安全测试",
		BusinessSystemID:  "11111111-1111-1111-1111-111111111111",
		TestType:          "penetration",
		ExpectedLaunchAt:  time.Now().AddDate(0, 1, 0),
		ContactUserID:     "22222222-2222-2222-2222-222222222222",
		Background:        "新系统对外提供服务，上线前需完成渗透测试",
		PublishedAssetIDs: []string{"33333333-3333-3333-3333-333333333333"},
	}
}

// uniqueTestUUID returns a deterministic valid uuid so scope-limit tests are
// not undone by normalizeInput's deduplication.
func uniqueTestUUID(index int) string {
	return fmt.Sprintf("33333333-3333-3333-3333-%012d", index)
}

func TestNormalizeAndValidateInput(t *testing.T) {
	input := validInputBase()
	input.RequestName = "  " + input.RequestName + "  "
	input.BusinessSystemID = " " + input.BusinessSystemID
	input.WorkingAssetIDs = []string{"", " 44444444-4444-4444-4444-444444444444 ", "44444444-4444-4444-4444-444444444444"}
	normalizeInput(&input)
	if input.RequestName != "客户服务平台二期上线安全测试" {
		t.Fatalf("request name was not trimmed: %q", input.RequestName)
	}
	if len(input.WorkingAssetIDs) != 1 {
		t.Fatalf("working asset ids were not deduplicated: %v", input.WorkingAssetIDs)
	}
	if !validInput(input) {
		t.Fatal("normalized input should be valid")
	}
}

func TestInputRequiresExactlyOneBusinessSystemReference(t *testing.T) {
	draft := validInputBase()
	draft.BusinessSystemID = ""
	draft.BusinessSystemDraftID = "55555555-5555-5555-5555-555555555555"
	if !validInput(draft) {
		t.Fatal("a working draft reference alone must be valid")
	}

	both := validInputBase()
	both.BusinessSystemDraftID = "55555555-5555-5555-5555-555555555555"
	if validInput(both) {
		t.Fatal("both published and draft references must be rejected")
	}

	neither := validInputBase()
	neither.BusinessSystemID = ""
	if validInput(neither) {
		t.Fatal("missing business system reference must be rejected")
	}
}

func TestInvalidInputDetails(t *testing.T) {
	base := validInputBase()

	tooMany := base
	tooMany.PublishedAssetIDs = nil
	for i := 0; i < maxScopeAssets+1; i++ {
		tooMany.PublishedAssetIDs = append(tooMany.PublishedAssetIDs, uniqueTestUUID(i))
	}
	normalizeInput(&tooMany)
	if validInput(tooMany) {
		t.Fatal("scope above the 200 item limit must be invalid")
	}

	emptyScope := base
	emptyScope.PublishedAssetIDs = nil
	if validInput(emptyScope) {
		t.Fatal("empty scope must be invalid")
	}

	badWindow := base
	start := time.Now().Add(48 * time.Hour)
	end := time.Now().Add(24 * time.Hour)
	badWindow.TestWindowStartAt, badWindow.TestWindowEndAt = &start, &end
	if validInput(badWindow) {
		t.Fatal("window ending before it starts must be invalid")
	}

	badType := base
	badType.TestType = "unknown"
	if validInput(badType) {
		t.Fatal("unknown test type must be invalid")
	}

	badContact := base
	badContact.ContactUserID = "not-a-uuid"
	if validInput(badContact) {
		t.Fatal("non-uuid contact must be invalid")
	}

	noBackground := base
	noBackground.Background = ""
	if validInput(noBackground) {
		t.Fatal("empty background must be invalid")
	}
}

func TestRequestAndBatchCodes(t *testing.T) {
	requestCode, err := newCode()
	if err != nil {
		t.Fatal(err)
	}
	batchCode, err := newBatchCode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(requestCode, "TR-"+time.Now().Format("20060102")+"-") {
		t.Fatalf("unexpected request code %q", requestCode)
	}
	if !strings.HasPrefix(batchCode, "ST-"+time.Now().Format("20060102")+"-") {
		t.Fatalf("unexpected batch code %q", batchCode)
	}
	other, err := newCode()
	if err != nil {
		t.Fatal(err)
	}
	if other == requestCode {
		t.Fatal("request codes must be unique")
	}
}

func TestStatusAndTypeValidation(t *testing.T) {
	for _, status := range []string{"draft", "submitted", "rejected", "testing", "pending_launch", "cancelled", "closed"} {
		if !validStatus(status) {
			t.Fatalf("status %q must be valid", status)
		}
	}
	for _, status := range []string{"", "authorized", "in_progress", "pending"} {
		if validStatus(status) {
			t.Fatalf("status %q must be invalid", status)
		}
	}
	if !validUUID("11111111-1111-1111-1111-111111111111") || validUUID("11111111-1111-1111-1111-11111111111X") || validUUID("") {
		t.Fatal("uuid validation is incorrect")
	}
}

func TestUniqueIDs(t *testing.T) {
	ids := uniqueIDs([]string{" a ", "", "b", "a", "  "})
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("uniqueIDs() = %v", ids)
	}
}

func TestPendingLaunchFrom(t *testing.T) {
	if pendingLaunchFrom("testing") != "testing" || pendingLaunchFrom("pending_launch") != "pending_launch" {
		t.Fatal("pendingLaunchFrom() must echo the source status for events")
	}
}

func TestRuleRecordTargetMissing(t *testing.T) {
	unknownSemantics := ruleRecord{fields: map[string]any{
		"network_scope": "unknown", "internet_exposure": "public", "value": "", "owner_name": "  ",
	}}
	if !ruleRecordTargetMissing(unknownSemantics, "network_scope") {
		t.Fatal("unknown network_scope must count as missing")
	}
	if ruleRecordTargetMissing(unknownSemantics, "internet_exposure") {
		t.Fatal("public internet_exposure must not count as missing")
	}
	if !ruleRecordTargetMissing(unknownSemantics, "value") || !ruleRecordTargetMissing(unknownSemantics, "owner_name") {
		t.Fatal("blank strings must count as missing")
	}
	asset := ruleRecord{fields: map[string]any{"has_business_system": false}}
	if !ruleRecordTargetMissing(asset, "business_system_id") {
		t.Fatal("unlinked asset must count as business_system_id missing")
	}
	custom := ruleRecord{custom: map[string]any{"level": nil}}
	if !ruleRecordTargetMissing(custom, "custom:level") {
		t.Fatal("missing custom field must count as missing")
	}
}

func TestRuleRecordConditionMatches(t *testing.T) {
	record := ruleRecord{fields: map[string]any{
		"environment": "production", "importance": "high", "active": true, "value": "example.com", "tags": []any{"web", "api"},
	}}
	cases := []struct {
		name      string
		condition dataquality.RuleCondition
		want      bool
	}{
		{"eq matches", dataquality.RuleCondition{Field: "environment", Operator: "eq", Values: []string{"production"}}, true},
		{"eq mismatch", dataquality.RuleCondition{Field: "environment", Operator: "eq", Values: []string{"test"}}, false},
		{"ne mismatch excluded", dataquality.RuleCondition{Field: "environment", Operator: "ne", Values: []string{"production"}}, false},
		{"in matches", dataquality.RuleCondition{Field: "importance", Operator: "in", Values: []string{"high", "medium"}}, true},
		{"in mismatch", dataquality.RuleCondition{Field: "importance", Operator: "in", Values: []string{"low"}}, false},
		{"not_in mismatch excluded", dataquality.RuleCondition{Field: "importance", Operator: "not_in", Values: []string{"high"}}, false},
		{"in across list values", dataquality.RuleCondition{Field: "tags", Operator: "in", Values: []string{"api"}}, true},
		{"is_true", dataquality.RuleCondition{Field: "active", Operator: "is_true"}, true},
		{"is_false mismatch", dataquality.RuleCondition{Field: "active", Operator: "is_false"}, false},
		{"not_empty", dataquality.RuleCondition{Field: "value", Operator: "not_empty"}, true},
		{"unknown operator", dataquality.RuleCondition{Field: "environment", Operator: "contains", Values: []string{"prod"}}, false},
	}
	for _, testCase := range cases {
		if got := ruleRecordConditionMatches(record, testCase.condition); got != testCase.want {
			t.Fatalf("%s: ruleRecordConditionMatches() = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestRuleRecordMatchesQualityRule(t *testing.T) {
	record := ruleRecord{resourceType: "asset", fields: map[string]any{
		"environment": "production", "owner_name": "",
	}}
	rule := dataquality.Rule{
		ResourceType: "asset", TargetField: "owner_name", RuleType: "required",
		Conditions: []dataquality.RuleCondition{{Field: "environment", Operator: "eq", Values: []string{"production"}}},
	}
	if !ruleRecordMatchesQualityRule(record, rule) {
		t.Fatal("missing owner on a production asset must match the rule")
	}
	rule.Conditions = append(rule.Conditions, dataquality.RuleCondition{Field: "environment", Operator: "eq", Values: []string{"test"}})
	if ruleRecordMatchesQualityRule(record, rule) {
		t.Fatal("a non-matching condition must disqualify the rule")
	}
}

func TestQualityValueHelpers(t *testing.T) {
	if !qualityValueMissing(nil) || !qualityValueMissing("  ") || !qualityValueMissing([]any{}) || qualityValueMissing("filled") || qualityValueMissing(0) {
		t.Fatal("qualityValueMissing() semantics are incorrect")
	}
	if !qualityValueContains([]string{"web", "api"}, []string{"api"}) || qualityValueContains("web", []string{"api"}) || !qualityValueContains([]any{1, 2}, []string{"2"}) {
		t.Fatal("qualityValueContains() semantics are incorrect")
	}
}

func TestDecodeCustomValues(t *testing.T) {
	custom, err := decodeCustomValues([]byte(`[{"field_key":"level","value":"core"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if custom["level"] != "core" {
		t.Fatalf("decodeCustomValues() = %v", custom)
	}
	if _, err := decodeCustomValues([]byte(`not json`)); err == nil {
		t.Fatal("invalid json must fail to decode")
	}
}

func TestScopeQualityErrorSummary(t *testing.T) {
	err := &ScopeQualityError{Check: ScopeQualityCheck{ErrorCount: 2}}
	if !strings.Contains(err.Error(), "2 error") {
		t.Fatalf("unexpected error message %q", err.Error())
	}
	findingsErr := &OpenFindingsError{Items: make([]BlockingFinding, 3)}
	if !strings.Contains(findingsErr.Error(), "3 unclosed finding") {
		t.Fatalf("unexpected error message %q", findingsErr.Error())
	}
	unbindableErr := &UnbindableScopeError{Count: 4}
	if !strings.Contains(unbindableErr.Error(), "4 scope working version") {
		t.Fatalf("unexpected error message %q", unbindableErr.Error())
	}
}
