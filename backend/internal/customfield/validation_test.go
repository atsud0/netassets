package customfield

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestDefinitionValidationPreservesAcceptedBoundaries(t *testing.T) {
	for _, resource := range []string{"asset", "business_system"} {
		for _, typ := range []string{"text", "textarea", "number", "date", "boolean", "select", "multiselect"} {
			for _, char := range []string{"名", "N", "😀"} {
				in := DefinitionInput{ResourceType: resource, FieldKey: "a" + strings.Repeat("b", 63), FieldName: strings.Repeat(char, 128), FieldType: typ, SortOrder: 100000}
				if typ == "select" || typ == "multiselect" {
					in.Options = make([]string, 100)
				}
				if err := validateDefinition(in); err != nil {
					t.Fatalf("%s/%s/%s: %v", resource, typ, char, err)
				}
				in.FieldName += char
				err := validateDefinition(in)
				var fieldErr *DefinitionValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &fieldErr) || fieldErr.Message != "字段名称最多 128 字" {
					t.Fatalf("unexpected validation: %v", err)
				}
			}
		}
	}
	for _, options := range [][]string{nil, make([]string, 101)} {
		in := DefinitionInput{ResourceType: "asset", FieldKey: "test", FieldName: "字段", FieldType: "select", Options: options}
		if err := validateDefinition(in); err == nil || err.Error() != "可选项须为 1 至 100 项" {
			t.Fatalf("invalid options: %v", err)
		}
	}
}
func TestDefinitionWritesRejectUnauthorizedBeforeValidation(t *testing.T) {
	for _, role := range []string{"organization_admin", "organization_editor", "security_operator", "vendor"} {
		s := NewService(nil)
		p := auth.Principal{Roles: []string{role}}
		_, createErr := s.Create(context.Background(), p, DefinitionInput{}, "", "")
		_, updateErr := s.Update(context.Background(), p, "invalid", DefinitionInput{}, "", "")
		if !errors.Is(createErr, ErrForbidden) || !errors.Is(updateErr, ErrForbidden) {
			t.Fatalf("%s: %v %v", role, createErr, updateErr)
		}
	}
}
