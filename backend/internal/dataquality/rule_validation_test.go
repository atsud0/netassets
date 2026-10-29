package dataquality

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestRuleValidationPreservesAcceptedBoundaries(t *testing.T) {
	svc := NewService(nil)
	for _, resource := range []string{"asset", "business_system"} {
		for _, char := range []string{"名", "N", "😀"} {
			for _, sortOrder := range []int{0, 100000} {
				in := Input{RuleName: strings.Repeat(char, 128), ResourceType: resource, RuleType: "required", TargetField: "owner_name", Severity: "error", SortOrder: sortOrder}
				if err := svc.valid(context.Background(), in); err != nil {
					t.Fatal(err)
				}
				in.RuleName += char
				err := svc.valid(context.Background(), in)
				var validation *RuleValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != "规则名称最多 128 字" {
					t.Fatalf("name: %v", err)
				}
			}
		}
		for _, days := range []int{1, 3650} {
			in := Input{RuleName: "合法天数", ResourceType: resource, RuleType: "stale", TargetField: "updated_at", Severity: "warning", StaleAfterDays: days}
			if err := svc.valid(context.Background(), in); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, days := range []int{0, 3650} {
		in := Input{RuleName: "到期", ResourceType: "asset", RuleType: "expiry", TargetField: "expires_at", Severity: "warning", ExpiryWarningDays: days}
		if err := svc.valid(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	// Preserve the existing limits on canonicalized conditions and byte-sized values.
	conditions := make([]RuleCondition, 10)
	for i := range conditions {
		conditions[i] = RuleCondition{Field: "organization_id", Operator: "eq", Values: []string{fmt.Sprintf("00000000-0000-0000-0000-%012d", i)}}
	}
	if err := svc.validateConditions(context.Background(), "asset", conditions); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		conditions []RuleCondition
		message    string
	}{
		{append(append([]RuleCondition{}, conditions...), conditions[0]), "生效条件最多 10 条"},
		{[]RuleCondition{conditions[0], conditions[0]}, "第 2 条生效条件与前面的条件重复"},
		{[]RuleCondition{{Field: "unknown", Operator: "eq", Values: []string{"a"}}}, "第 1 条生效条件请选择有效字段"},
		{[]RuleCondition{{Field: strings.Repeat("名", 43), Operator: "empty"}}, "第 1 条生效条件的字段和运算符不能为空，字段最多 128 字节"},
		{[]RuleCondition{{Field: "organization_id", Operator: "eq", Values: []string{"not-a-uuid"}}}, "第 1 条生效条件：条件值中的组织或网络区域标识格式不正确"},
		{[]RuleCondition{{Field: "organization_id", Operator: "eq", Values: []string{strings.Repeat("x", 257)}}}, "第 1 条生效条件：条件值不能超过 256 字节"},
	} {
		err := svc.validateConditions(context.Background(), "asset", tc.conditions)
		var validation *RuleValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != tc.message {
			t.Fatalf("conditions: %v; want %s", err, tc.message)
		}
	}
}

func TestRuleWritesRejectUnauthorizedBeforeValidation(t *testing.T) {
	for _, role := range []string{"organization_admin", "organization_editor", "security_operator", "vendor"} {
		svc := NewService(nil)
		p := auth.Principal{Roles: []string{role}}
		// A custom target and field would require database access if authorization
		// did not run first. A nil pool makes that regression visible.
		in := Input{RuleName: "规则", ResourceType: "asset", RuleType: "required", TargetField: "custom:missing", Severity: "error", Conditions: []RuleCondition{{Field: "custom:missing", Operator: "empty"}}}
		if _, err := svc.Create(context.Background(), p, in, "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s create: %v", role, err)
		}
		if _, err := svc.Update(context.Background(), p, "invalid-id", in, "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s update: %v", role, err)
		}
	}
}
