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
