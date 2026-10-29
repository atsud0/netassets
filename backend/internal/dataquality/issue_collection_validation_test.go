package dataquality

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIssueFilterValidationNormalizesAcceptedValues(t *testing.T) {
	for _, status := range []string{"", "pending", "in_progress", "pending_review", "closed", "exempted"} {
		for _, assignee := range []string{"", "me", batchUserID} {
			filter := IssueFilter{OrganizationID: " " + batchIssueID1 + " ", Status: " " + status + " ", Severity: " warning ", ResourceType: " business_system ", Assignee: " " + assignee + " ", Limit: 501}
			if err := validateIssueFilter(&filter); err != nil {
				t.Fatal(err)
			}
			if filter.OrganizationID != batchIssueID1 || filter.Status != status || filter.Severity != "warning" || filter.ResourceType != "business_system" || filter.Assignee != assignee || filter.Limit != 500 || filter.Page.PageSize != 100 {
				t.Fatalf("unexpected normalization: %+v", filter)
			}
		}
	}
}

func TestBatchIssueValidationPreservesUnicodeAndSelectionBoundaries(t *testing.T) {
	ids := make([]string, MaxIssueBatchItems)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
	}
	for _, char := range []string{"中", "N", "😀"} {
		input := BatchIssueInput{IDs: ids, Action: " SUBMIT ", Note: "　" + strings.Repeat(char, 5000) + " "}
		normalized, action, _, note, err := normalizeBatchIssueInput(input)
		if err != nil || len(normalized) != MaxIssueBatchItems || action != "submit" || note != strings.Repeat(char, 5000) {
			t.Fatalf("maximum %q boundary: %v", char, err)
		}
		input.Note = strings.Repeat(char, 5001)
		_, _, _, _, err = normalizeBatchIssueInput(input)
		var validation *IssueValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != "处理说明最多 5000 字" {
			t.Fatalf("oversized %q message: %v", char, err)
		}
	}
	for _, action := range []string{"assign", "start"} {
		input := BatchIssueInput{IDs: []string{" " + batchIssueID1 + " ", batchIssueID1}, Action: " " + strings.ToUpper(action) + " ", AssigneeUserID: " " + batchUserID + " "}
		normalized, gotAction, assignee, note, err := normalizeBatchIssueInput(input)
		if err != nil || len(normalized) != 1 || normalized[0] != batchIssueID1 || gotAction != action || assignee != batchUserID || note != "" {
			t.Fatalf("optional note and normalized selection: %v", err)
		}
	}
}
