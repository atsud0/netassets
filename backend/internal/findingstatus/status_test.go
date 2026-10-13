package findingstatus

import "testing"

func TestDefinitionsCoverEveryStatus(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 9 {
		t.Fatalf("expected 9 statuses, got %d", len(definitions))
	}
	seen := map[string]bool{}
	orders := map[int]bool{}
	for _, definition := range definitions {
		if seen[definition.Value] {
			t.Fatalf("duplicate status value %q", definition.Value)
		}
		if orders[definition.SortOrder] {
			t.Fatalf("duplicate sort order %d", definition.SortOrder)
		}
		if definition.Label == "" {
			t.Fatalf("status %q has no label", definition.Value)
		}
		seen[definition.Value] = true
		orders[definition.SortOrder] = true
		if !Valid(definition.Value) {
			t.Fatalf("status %q is not accepted by Valid", definition.Value)
		}
		if Label(definition.Value) != definition.Label {
			t.Fatalf("label mismatch for %q", definition.Value)
		}
	}
	if Valid("unknown") {
		t.Fatal("unknown status accepted")
	}
	if Label("unknown") != "unknown" {
		t.Fatal("unknown status should fall back to raw value")
	}
}

func TestClosedSemantics(t *testing.T) {
	closed := []string{Resolved, Fixed, FalsePositive, Ignored, Duplicate, RiskAccepted}
	for _, status := range closed {
		if !Closed(status) {
			t.Fatalf("status %q should be closed", status)
		}
		if Open(status) {
			t.Fatalf("status %q should not be open", status)
		}
		if !contains(ClosedSQL(), "'"+status+"'") {
			t.Fatalf("ClosedSQL is missing %q", status)
		}
	}
	open := []string{Pending, InProgress, Confirmed}
	for _, status := range open {
		if Closed(status) {
			t.Fatalf("status %q should not be closed", status)
		}
		if !Open(status) {
			t.Fatalf("status %q should be open", status)
		}
		if contains(ClosedSQL(), "'"+status+"'") {
			t.Fatalf("ClosedSQL should not include open status %q", status)
		}
	}
	if Closed("unknown") || Open("unknown") {
		t.Fatal("unknown status must be neither closed nor open")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestCanTransition(t *testing.T) {
	allowed := [][2]string{
		{Pending, Confirmed},
		{Confirmed, InProgress},
		{InProgress, Resolved},
		{Resolved, Fixed},
		{FalsePositive, Confirmed},
		{Duplicate, Pending},
	}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("CanTransition(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	blocked := [][2]string{
		{Pending, Fixed},
		{Fixed, RiskAccepted},
		{Confirmed, RiskAccepted},
		{RiskAccepted, Confirmed},
		{"unknown", Pending},
	}
	for _, pair := range blocked {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("CanTransition(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
	if !CanSetDirectly(Confirmed) || CanSetDirectly(RiskAccepted) || CanSetDirectly(Duplicate) {
		t.Fatal("direct status policy does not protect evidence-bearing states")
	}
}
