package customfield

import (
	"strings"
	"testing"
)

func TestFieldNameCharacterLimit(t *testing.T) {
	for _, resource := range []string{"asset", "business_system"} {
		in := DefinitionInput{ResourceType: resource, FieldKey: "test_name", FieldName: strings.Repeat("名", 128), FieldType: "text"}
		if !valid(in) {
			t.Fatalf("128 Chinese characters should be valid for %s", resource)
		}
		in.FieldName += "名"
		if valid(in) {
			t.Fatalf("129 characters should be invalid for %s", resource)
		}
	}
}
