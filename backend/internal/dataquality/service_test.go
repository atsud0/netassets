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
