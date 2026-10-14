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

// The external scanner export the customer produces carries this vocabulary, and
// `notify`-style labels shown to reviewers are derived from it. A silent change
// to either a value or its Chinese label would corrupt both the import mapping
// and every status breakdown, so the contract is pinned literally rather than
// derived from the implementation.
//
// Note the deliberate distinction: resolved = 已处理 (the remediation work was
// carried out) and fixed = 已修复 (the vulnerability no longer reproduces).
// They are separate states and must never be collapsed into one another.
func TestStatusVocabularyMatchesExternalExportContract(t *testing.T) {
	want := []struct {
		value string
		label string
	}{
		{Pending, "待处理"},
		{InProgress, "处理中"},
		{Confirmed, "已确认"},
		{Resolved, "已处理"},
		{Fixed, "已修复"},
		{FalsePositive, "误报"},
		{Ignored, "忽略"},
		{Duplicate, "重复"},
		{RiskAccepted, "风险接受"},
	}
	definitions := Definitions()
	if len(definitions) != len(want) {
		t.Fatalf("status count = %d, want %d", len(definitions), len(want))
	}
	for i, expected := range want {
		if definitions[i].Value != expected.value || definitions[i].Label != expected.label {
			t.Fatalf("status %d = (%q, %q), want (%q, %q)", i, definitions[i].Value, definitions[i].Label, expected.value, expected.label)
		}
	}
	if Label(Resolved) == Label(Fixed) {
		t.Fatal("resolved and fixed share one label; the two states are no longer distinguishable to a reviewer")
	}
	// The two states must also stay distinguishable in the flow direction: a
	// handled finding becomes fixed only through an explicit transition, never
	// by being relabelled.
	if CanTransition(Resolved, Fixed) != true || CanTransition(Fixed, Resolved) {
		t.Fatal("resolved/fixed transition direction changed; handled findings must be able to become fixed without rewriting history")
	}
}
