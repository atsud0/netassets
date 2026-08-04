package inventorytask

import (
	"testing"

	"netassets/internal/dataquality"
)

func TestBusinessSystemDraftMatchesConfiguredRequiredRule(t *testing.T) {
	record := businessSystemDraftQualityRecord{
		fields: map[string]any{
			"owner_contact": "",
			"system_level":  "important",
		},
		custom: map[string]any{},
	}
	rule := dataquality.Rule{
		TargetField: "owner_contact",
		Conditions: []dataquality.RuleCondition{{
			Field: "system_level", Operator: "in", Values: []string{"important", "core"},
		}},
	}
	if !businessSystemDraftMatchesQualityRule(record, rule) {
		t.Fatal("expected important system with missing owner contact to match")
	}
	record.fields["system_level"] = "normal"
	if businessSystemDraftMatchesQualityRule(record, rule) {
		t.Fatal("normal system should not match the conditional required rule")
	}
}

func TestBusinessSystemDraftMatchesCustomRequiredRule(t *testing.T) {
	record := businessSystemDraftQualityRecord{
		fields: map[string]any{"internet_exposure": "yes"},
		custom: map[string]any{"reviewed": true},
	}
	rule := dataquality.Rule{
		TargetField: "custom:owner_department",
		Conditions: []dataquality.RuleCondition{
			{Field: "internet_exposure", Operator: "eq", Values: []string{"yes"}},
			{Field: "custom:reviewed", Operator: "is_true"},
		},
	}
	if !businessSystemDraftMatchesQualityRule(record, rule) {
		t.Fatal("expected missing custom field to match conditional rule")
	}
	record.custom["owner_department"] = "信息技术部"
	if businessSystemDraftMatchesQualityRule(record, rule) {
		t.Fatal("filled custom field should not match required rule")
	}
}

func TestBusinessSystemDraftMatchesUnknownEnumerationTarget(t *testing.T) {
	record := businessSystemDraftQualityRecord{
		fields: map[string]any{"internet_exposure": "unknown"},
		custom: map[string]any{},
	}
	if !businessSystemDraftMatchesQualityRule(record, dataquality.Rule{TargetField: "internet_exposure"}) {
		t.Fatal("unknown internet exposure should be treated as missing")
	}
	record.fields["internet_exposure"] = "no"
	if businessSystemDraftMatchesQualityRule(record, dataquality.Rule{TargetField: "internet_exposure"}) {
		t.Fatal("known internet exposure should not be treated as missing")
	}
}
