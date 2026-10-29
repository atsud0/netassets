package dataquality

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRuleNameCharacterLimit(t *testing.T) {
	s := &Service{}
	in := Input{RuleName: strings.Repeat("规", 128), ResourceType: "asset", RuleType: "required", TargetField: "owner_name", Severity: "warning"}
	if err := s.valid(context.Background(), in); err != nil {
		t.Fatalf("128 Chinese characters rejected: %v", err)
	}
	in.RuleName += "规"
	if err := s.valid(context.Background(), in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("129 characters: expected ErrInvalid, got %v", err)
	}
}
