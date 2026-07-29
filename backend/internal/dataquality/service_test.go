package dataquality

import (
	"context"
	"errors"
	"testing"
)

func TestValidStaleRule(t *testing.T) {
	svc := &Service{}
	valid := Input{
		RuleName: "资产超过90天未维护", ResourceType: "asset", RuleType: "stale",
		TargetField: "updated_at", Severity: "warning", StaleAfterDays: 90,
	}
	if err := svc.valid(context.Background(), valid); err != nil {
		t.Fatalf("valid stale rule rejected: %v", err)
	}

	invalid := []Input{
		{RuleName: "无天数", ResourceType: "asset", RuleType: "stale", TargetField: "updated_at", Severity: "warning"},
		{RuleName: "天数过大", ResourceType: "asset", RuleType: "stale", TargetField: "updated_at", Severity: "warning", StaleAfterDays: 3651},
		{RuleName: "错误字段", ResourceType: "asset", RuleType: "stale", TargetField: "owner_name", Severity: "warning", StaleAfterDays: 90},
		{RuleName: "必填规则不应有天数", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "error", StaleAfterDays: 90},
	}
	for _, input := range invalid {
		if err := svc.valid(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input=%#v err=%v, want ErrInvalid", input, err)
		}
	}
}

func TestValidExpiryRule(t *testing.T) {
	svc := &Service{}
	valid := Input{
		RuleName: "资产到期前30天提醒", ResourceType: "asset", RuleType: "expiry",
		TargetField: "expires_at", Severity: "warning", ExpiryWarningDays: 30,
	}
	if err := svc.valid(context.Background(), valid); err != nil {
		t.Fatalf("valid expiry rule rejected: %v", err)
	}
	if err := svc.valid(context.Background(), Input{RuleName: "到期当天提醒", ResourceType: "asset", RuleType: "expiry", TargetField: "expires_at", Severity: "error", ExpiryWarningDays: 0}); err != nil {
		t.Fatalf("zero-day expiry rule rejected: %v", err)
	}

	invalid := []Input{
		{RuleName: "系统到期", ResourceType: "business_system", RuleType: "expiry", TargetField: "expires_at", Severity: "warning", ExpiryWarningDays: 30},
		{RuleName: "错误字段", ResourceType: "asset", RuleType: "expiry", TargetField: "updated_at", Severity: "warning", ExpiryWarningDays: 30},
		{RuleName: "负数天数", ResourceType: "asset", RuleType: "expiry", TargetField: "expires_at", Severity: "warning", ExpiryWarningDays: -1},
		{RuleName: "天数过大", ResourceType: "asset", RuleType: "expiry", TargetField: "expires_at", Severity: "warning", ExpiryWarningDays: 3651},
		{RuleName: "混用陈旧阈值", ResourceType: "asset", RuleType: "expiry", TargetField: "expires_at", Severity: "warning", StaleAfterDays: 30, ExpiryWarningDays: 30},
		{RuleName: "必填规则混入到期阈值", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "error", ExpiryWarningDays: 30},
		{RuleName: "陈旧规则混入到期阈值", ResourceType: "asset", RuleType: "stale", TargetField: "updated_at", Severity: "warning", StaleAfterDays: 90, ExpiryWarningDays: 30},
	}
	for _, input := range invalid {
		if err := svc.valid(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input=%#v err=%v, want ErrInvalid", input, err)
		}
	}
}

func TestNormalizeRuleConditions(t *testing.T) {
	input := Input{Conditions: []RuleCondition{
		{Field: " Network_Scope ", Operator: " IN ", Values: []string{" public ", "public", "private", ""}},
		{Field: " environment ", Operator: " EQ ", Values: []string{" production "}},
	}}
	normalize(&input)
	if input.Conditions[0].Field != "environment" || input.Conditions[0].Operator != "eq" {
		t.Fatalf("conditions not canonically sorted: %#v", input.Conditions)
	}
	condition := input.Conditions[1]
	if condition.Field != "network_scope" || condition.Operator != "in" {
		t.Fatalf("condition not normalized: %#v", condition)
	}
	if len(condition.Values) != 2 || condition.Values[0] != "private" || condition.Values[1] != "public" {
		t.Fatalf("condition values not normalized and sorted: %#v", condition.Values)
	}
}

func TestValidateBuiltinRuleConditions(t *testing.T) {
	svc := &Service{}
	valid := []Input{
		{RuleName: "公网资产负责人必填", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "error", Conditions: []RuleCondition{{Field: "network_scope", Operator: "eq", Values: []string{"public"}}}},
		{RuleName: "生产高重要资产关联系统", ResourceType: "asset", RuleType: "required", TargetField: "business_system_id", Severity: "error", Conditions: []RuleCondition{{Field: "environment", Operator: "eq", Values: []string{"production"}}, {Field: "importance", Operator: "in", Values: []string{"important", "core"}}}},
		{RuleName: "个人信息系统维护检查", ResourceType: "business_system", RuleType: "stale", TargetField: "updated_at", Severity: "warning", StaleAfterDays: 90, Conditions: []RuleCondition{{Field: "contains_personal_information", Operator: "eq", Values: []string{"yes"}}}},
		{RuleName: "未关联系统资产", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "warning", Conditions: []RuleCondition{{Field: "has_business_system", Operator: "is_false"}}},
	}
	for _, input := range valid {
		if err := svc.valid(context.Background(), input); err != nil {
			t.Fatalf("input=%#v rejected: %v", input, err)
		}
	}
}

func TestRejectInvalidRuleConditions(t *testing.T) {
	svc := &Service{}
	base := Input{RuleName: "条件规则", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "error"}
	tests := [][]RuleCondition{
		{{Field: "internet_exposure", Operator: "eq", Values: []string{"yes"}}},
		{{Field: "network_scope", Operator: "contains", Values: []string{"public"}}},
		{{Field: "network_scope", Operator: "eq"}},
		{{Field: "network_scope", Operator: "empty", Values: []string{"public"}}},
		{{Field: "network_scope", Operator: "eq", Values: []string{"external"}}},
		{{Field: "organization_id", Operator: "eq", Values: []string{"not-a-uuid"}}},
		{{Field: "has_business_system", Operator: "is_true", Values: []string{"true"}}},
	}
	for _, conditions := range tests {
		input := base
		input.Conditions = conditions
		if err := svc.valid(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("conditions=%#v err=%v, want ErrInvalid", conditions, err)
		}
	}
}

func TestRejectTooManyAndDuplicateRuleConditions(t *testing.T) {
	svc := &Service{}
	base := Input{RuleName: "条件规则", ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "error"}
	for i := 0; i < maxRuleConditions+1; i++ {
		base.Conditions = append(base.Conditions, RuleCondition{Field: "environment", Operator: "eq", Values: []string{"production"}})
	}
	if err := svc.valid(context.Background(), base); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many conditions err=%v, want ErrInvalid", err)
	}

	base.Conditions = []RuleCondition{
		{Field: "environment", Operator: "eq", Values: []string{"production"}},
		{Field: "environment", Operator: "eq", Values: []string{"production"}},
	}
	if err := svc.valid(context.Background(), base); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate conditions err=%v, want ErrInvalid", err)
	}
}

func TestValidateConditionAllowedValuesForCustomChoice(t *testing.T) {
	spec := conditionFieldSpec{kind: "multiselect", allowedValues: stringSet("个人信息", "重要数据")}
	if err := validConditionValues(RuleCondition{Operator: "in", Values: []string{"个人信息"}}, spec); err != nil {
		t.Fatalf("configured option rejected: %v", err)
	}
	if err := validConditionValues(RuleCondition{Operator: "in", Values: []string{"任意输入"}}, spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown option err=%v, want ErrInvalid", err)
	}
}
