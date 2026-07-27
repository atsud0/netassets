package notification

import "testing"

func TestValidateCategory(t *testing.T) {
	for _, category := range []string{"", "work_order_review", "work_order_received", "work_order_returned", "work_order_overdue", "work_order_due_soon", "risk_review"} {
		if !ValidateCategory(category) {
			t.Fatalf("ValidateCategory(%q) = false", category)
		}
	}
	for _, category := range []string{"unknown", "risk", "work_order"} {
		if ValidateCategory(category) {
			t.Fatalf("ValidateCategory(%q) = true", category)
		}
	}
}
