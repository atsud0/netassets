package securityworkorder

import (
	"context"
	"errors"
	"netassets/internal/auth"
	"testing"
)

func TestWorkOrderItemInputValidation(test *testing.T) {
	service := &Service{}
	base := WorkOrderItemInput{Items: []WorkOrderItemVersion{{ID: "00000000-0000-0000-0000-000000000001", Version: 1}}, Action: "progress", Note: "有效说明"}
	for _, name := range []string{"missing_note", "unknown_action", "empty_selection", "duplicate_selection", "missing_evidence", "invalid_evidence", "duplicate_evidence"} {
		test.Run(name, func(test *testing.T) {
			input := base
			switch name {
			case "missing_note":
				input.Note = " "
			case "unknown_action":
				input.Action = "fixed"
			case "empty_selection":
				input.Items = nil
			case "duplicate_selection":
				input.Items = append(input.Items, input.Items[0])
			case "missing_evidence":
				input.Action = "submit"
			case "invalid_evidence":
				input.EvidenceIDs = []string{"invalid"}
			case "duplicate_evidence":
				input.EvidenceIDs = []string{base.Items[0].ID, base.Items[0].ID}
			}
			_, err := service.ProcessWorkOrderItems(context.Background(), auth.Principal{}, base.Items[0].ID, input, "", "")
			if !errors.Is(err, ErrInvalid) {
				test.Fatalf("expected invalid before database access, got %v", err)
			}
		})
	}
}

func TestItemStageTransitions(test *testing.T) {
	for _, stage := range []string{"not_started", "in_progress", "pending_verification", "verified", "returned", "disposition"} {
		for _, action := range []string{"progress", "submit", "pass", "return", "fixed", "unknown"} {
			next, err := nextItemStage(stage, action)
			allowed := ((action == "progress" || action == "submit") && (stage == "not_started" || stage == "in_progress" || stage == "returned")) || ((action == "pass" || action == "return") && stage == "pending_verification")
			if (err == nil) != allowed || (err == nil && next == "") {
				test.Errorf("%s/%s: %s %v", stage, action, next, err)
			}
		}
	}
}
