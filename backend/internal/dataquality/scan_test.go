package dataquality

import (
	"errors"
	"strings"
	"testing"
)

func TestScanQueryPartsAssetBuiltin(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "required", ResourceType: "asset", TargetField: "owner_name"}, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "asset_organizations") || !strings.Contains(from, "NULLIF($1::text,'')") {
		t.Fatalf("unexpected asset from clause: %s", from)
	}
	if condition != "btrim(COALESCE(ao.owner_name,''))=''" {
		t.Fatalf("condition = %q", condition)
	}
	if len(args) != 1 || args[0] != "organization-id" {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsBusinessSystemBuiltin(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "required", ResourceType: "business_system", TargetField: "internet_exposure"}, "")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "business_system_data_security_profiles") {
		t.Fatalf("unexpected business system from clause: %s", from)
	}
	if condition != "bs.internet_exposure='unknown'" {
		t.Fatalf("condition = %q", condition)
	}
	if len(args) != 1 || args[0] != "" {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsCustomField(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "required", ResourceType: "asset", TargetField: "custom:data_level"}, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "FROM assets") || !strings.Contains(condition, "custom_field_values") || !strings.Contains(condition, "d.field_key=$2") {
		t.Fatalf("unexpected custom field query parts: from=%q condition=%q", from, condition)
	}
	if len(args) != 2 || args[0] != "organization-id" || args[1] != "data_level" {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsRejectsUnsupportedTarget(t *testing.T) {
	_, _, _, err := scanQueryParts(Rule{RuleType: "required", ResourceType: "asset", TargetField: "unsupported"}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported asset quality target") {
		t.Fatalf("err = %v", err)
	}
}

func TestScanQueryPartsRejectsUnsupportedResourceType(t *testing.T) {
	_, _, _, err := scanQueryParts(Rule{RuleType: "required", ResourceType: "unknown", TargetField: "name"}, "")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestScanQueryPartsAssetStale(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "stale", ResourceType: "asset", TargetField: "updated_at", StaleAfterDays: 90}, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "FROM assets") || condition != "a.updated_at < now() - ($2::integer * interval '1 day')" {
		t.Fatalf("unexpected stale asset query parts: from=%q condition=%q", from, condition)
	}
	if len(args) != 2 || args[0] != "organization-id" || args[1] != 90 {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsBusinessSystemStale(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "stale", ResourceType: "business_system", TargetField: "updated_at", StaleAfterDays: 180}, "")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "FROM business_systems") || condition != "bs.updated_at < now() - ($2::integer * interval '1 day')" {
		t.Fatalf("unexpected stale business system query parts: from=%q condition=%q", from, condition)
	}
	if len(args) != 2 || args[0] != "" || args[1] != 180 {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsAssetExpiry(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{RuleType: "expiry", ResourceType: "asset", TargetField: "expires_at", ExpiryWarningDays: 30}, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(from, "FROM assets") || condition != "a.status<>'archived' AND a.expires_at IS NOT NULL AND a.expires_at <= now() + ($2::integer * interval '1 day')" {
		t.Fatalf("unexpected expiry asset query parts: from=%q condition=%q", from, condition)
	}
	if len(args) != 2 || args[0] != "organization-id" || args[1] != 30 {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsRejectsInvalidExpiryRule(t *testing.T) {
	tests := []Rule{
		{RuleType: "expiry", ResourceType: "asset", TargetField: "updated_at", ExpiryWarningDays: 30},
		{RuleType: "expiry", ResourceType: "asset", TargetField: "expires_at", ExpiryWarningDays: -1},
		{RuleType: "expiry", ResourceType: "business_system", TargetField: "expires_at", ExpiryWarningDays: 30},
	}
	for _, rule := range tests {
		if _, _, _, err := scanQueryParts(rule, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rule=%#v err=%v, want ErrInvalid", rule, err)
		}
	}
}

func TestScanQueryPartsRejectsInvalidStaleRule(t *testing.T) {
	tests := []Rule{
		{RuleType: "stale", ResourceType: "asset", TargetField: "owner_name", StaleAfterDays: 90},
		{RuleType: "stale", ResourceType: "asset", TargetField: "updated_at", StaleAfterDays: 0},
		{RuleType: "stale", ResourceType: "business_system", TargetField: "updated_at", StaleAfterDays: 3651},
	}
	for _, rule := range tests {
		if _, _, _, err := scanQueryParts(rule, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rule=%#v err=%v, want ErrInvalid", rule, err)
		}
	}
}

func TestScanQueryPartsAppliesAssetConditionsWithDynamicParameters(t *testing.T) {
	rule := Rule{
		RuleType: "required", ResourceType: "asset", TargetField: "owner_name",
		Conditions: []RuleCondition{
			{Field: "network_scope", Operator: "eq", Values: []string{"public"}},
			{Field: "importance", Operator: "in", Values: []string{"important", "core"}},
		},
	}
	_, condition, args, err := scanQueryParts(rule, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(condition, "a.network_scope") || !strings.Contains(condition, "$2") || !strings.Contains(condition, "a.importance") || !strings.Contains(condition, "$3::text[]") || strings.Count(condition, " AND ") < 2 {
		t.Fatalf("unexpected condition: %s", condition)
	}
	if len(args) != 3 || args[1] != "public" {
		t.Fatalf("args = %#v", args)
	}
	values, ok := args[2].([]string)
	if !ok || len(values) != 2 || values[1] != "core" {
		t.Fatalf("multi-value args = %#v", args[2])
	}
}

func TestScanQueryPartsStaleConditionParameterFollowsThreshold(t *testing.T) {
	rule := Rule{
		RuleType: "stale", ResourceType: "business_system", TargetField: "updated_at", StaleAfterDays: 90,
		Conditions: []RuleCondition{{Field: "contains_personal_information", Operator: "eq", Values: []string{"yes"}}},
	}
	_, condition, args, err := scanQueryParts(rule, "")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(condition, "$2::integer") || !strings.Contains(condition, "p.contains_personal_information") || !strings.Contains(condition, "$3") {
		t.Fatalf("unexpected condition: %s", condition)
	}
	if len(args) != 3 || args[1] != 90 || args[2] != "yes" {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsAssetBusinessSystemPresence(t *testing.T) {
	tests := []struct {
		operator string
		want     string
	}{
		{operator: "is_true", want: "EXISTS (SELECT 1 FROM system_assets"},
		{operator: "is_false", want: "NOT EXISTS (SELECT 1 FROM system_assets"},
	}
	for _, test := range tests {
		rule := Rule{RuleType: "required", ResourceType: "asset", TargetField: "owner_name", Conditions: []RuleCondition{{Field: "has_business_system", Operator: test.operator}}}
		_, condition, _, err := scanQueryParts(rule, "")
		if err != nil {
			t.Fatalf("operator=%s error=%v", test.operator, err)
		}
		if !strings.Contains(condition, test.want) {
			t.Fatalf("operator=%s condition=%s", test.operator, condition)
		}
	}
}

func TestScanQueryPartsCustomConditionUsesOnlyParameters(t *testing.T) {
	malicious := `critical' OR true --`
	rule := Rule{
		RuleType: "required", ResourceType: "asset", TargetField: "custom:data_owner",
		Conditions: []RuleCondition{{Field: "custom:data_level", Operator: "eq", Values: []string{malicious}}},
	}
	_, condition, args, err := scanQueryParts(rule, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if strings.Contains(condition, malicious) || !strings.Contains(condition, "d.field_key=$3") || !strings.Contains(condition, "$4") {
		t.Fatalf("unsafe or unexpected condition: %s", condition)
	}
	if len(args) != 4 || args[1] != "data_owner" || args[2] != "data_level" || args[3] != malicious {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanQueryPartsCustomMultiselectCondition(t *testing.T) {
	rule := Rule{
		RuleType: "required", ResourceType: "business_system", TargetField: "owner_contact",
		Conditions: []RuleCondition{{Field: "custom:data_categories", Operator: "in", Values: []string{"个人信息", "重要数据"}}},
	}
	_, condition, args, err := scanQueryParts(rule, "")
	if err != nil {
		t.Fatalf("scanQueryParts returned error: %v", err)
	}
	if !strings.Contains(condition, "jsonb_array_elements_text") || !strings.Contains(condition, "$3::text[]") {
		t.Fatalf("unexpected condition: %s", condition)
	}
	if len(args) != 3 || args[1] != "data_categories" {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildRuleIssueCountQueryUsesScanSemantics(t *testing.T) {
	query, args, err := BuildRuleIssueCountQuery(Rule{
		RuleType: "required", ResourceType: "asset", TargetField: "owner_name",
		Conditions: []RuleCondition{{Field: "network_scope", Operator: "eq", Values: []string{"public"}}},
	}, "organization-id")
	if err != nil {
		t.Fatalf("BuildRuleIssueCountQuery returned error: %v", err)
	}
	if !strings.HasPrefix(query, "SELECT count(*) FROM assets") || !strings.Contains(query, "a.network_scope") || len(args) != 2 {
		t.Fatalf("query=%s args=%#v", query, args)
	}
}

func TestScanQueryPartsRejectsUnsupportedConditionField(t *testing.T) {
	_, _, _, err := scanQueryParts(Rule{
		RuleType: "required", ResourceType: "asset", TargetField: "owner_name",
		Conditions: []RuleCondition{{Field: "raw_sql", Operator: "eq", Values: []string{"1=1"}}},
	}, "")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

func TestScalarNegativeConditionsExcludeBlankValues(t *testing.T) {
	for _, operator := range []string{"ne", "not_in"} {
		values := []string{"production"}
		query, _, err := scalarConditionSQL("bs.deployment_type", RuleCondition{Operator: operator, Values: values}, nil)
		if err != nil {
			t.Fatalf("operator=%s error=%v", operator, err)
		}
		if !strings.Contains(query, "IS NOT NULL AND") {
			t.Fatalf("operator=%s should exclude blank values: %s", operator, query)
		}
	}
}

func TestScanQueryPartsExternalDomainExpiry(t *testing.T) {
	from, condition, args, err := scanQueryParts(Rule{
		RuleType: "expiry", ResourceType: "asset", TargetField: "domain_registration_expires_at", ExpiryWarningDays: 30,
	}, "organization-id")
	if err != nil {
		t.Fatalf("scanQueryParts() error = %v", err)
	}
	if !strings.Contains(from, "FROM assets a") || !strings.Contains(condition, "asset_external_observations") || !strings.Contains(condition, "e.status='success'") {
		t.Fatalf("unexpected external domain expiry query parts: from=%q condition=%q", from, condition)
	}
	if len(args) != 3 || args[1] != 30 || args[2] != "domain_registration" {
		t.Fatalf("unexpected external domain expiry args: %#v", args)
	}
}

func TestScanQueryPartsExternalTLSExpiry(t *testing.T) {
	_, condition, args, err := scanQueryParts(Rule{
		RuleType: "expiry", ResourceType: "asset", TargetField: "tls_certificate_expires_at", ExpiryWarningDays: 7,
	}, "")
	if err != nil {
		t.Fatalf("scanQueryParts() error = %v", err)
	}
	if !strings.Contains(condition, "asset_external_observations") || len(args) != 3 || args[2] != "tls_certificate" {
		t.Fatalf("unexpected TLS expiry query: condition=%q args=%#v", condition, args)
	}
}
