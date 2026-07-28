package dataquality

import (
	"errors"
	"strings"
	"testing"
)

const (
	batchIssueID1 = "550e8400-e29b-41d4-a716-446655440000"
	batchIssueID2 = "550e8400-e29b-41d4-a716-446655440001"
	batchUserID   = "550e8400-e29b-41d4-a716-446655440002"
)

func TestNormalizeBatchIssueInput(t *testing.T) {
	tests := []struct {
		name       string
		input      BatchIssueInput
		wantIDs    int
		wantAction string
		wantNote   string
		wantErr    bool
	}{
		{name: "assign", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: " ASSIGN ", AssigneeUserID: batchUserID, Note: "  责任人调整  "}, wantIDs: 1, wantAction: BatchIssueActionAssign, wantNote: "责任人调整"},
		{name: "start deduplicates", input: BatchIssueInput{IDs: []string{batchIssueID1, batchIssueID1, batchIssueID2}, Action: "start", AssigneeUserID: batchUserID}, wantIDs: 2, wantAction: BatchIssueActionStart},
		{name: "submit", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "submit", Note: "已完成整改"}, wantIDs: 1, wantAction: BatchIssueActionSubmit, wantNote: "已完成整改"},
		{name: "empty ids", input: BatchIssueInput{Action: "submit", Note: "已完成整改"}, wantErr: true},
		{name: "invalid id", input: BatchIssueInput{IDs: []string{"not-a-uuid"}, Action: "submit", Note: "已完成整改"}, wantErr: true},
		{name: "invalid action", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "approve"}, wantErr: true},
		{name: "missing assignee", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "start"}, wantErr: true},
		{name: "invalid assignee", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "assign", AssigneeUserID: "invalid"}, wantErr: true},
		{name: "missing submit note", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "submit", Note: "  "}, wantErr: true},
		{name: "note too long", input: BatchIssueInput{IDs: []string{batchIssueID1}, Action: "submit", Note: strings.Repeat("字", 5001)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, action, _, note, err := normalizeBatchIssueInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBatchIssueInput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("error = %v, want ErrInvalid", err)
				}
				return
			}
			if len(ids) != tt.wantIDs || action != tt.wantAction || note != tt.wantNote {
				t.Fatalf("got ids=%v action=%q note=%q", ids, action, note)
			}
		})
	}
}

func TestNormalizeBatchIssueInputRejectsMoreThanMaximum(t *testing.T) {
	ids := make([]string, MaxIssueBatchItems+1)
	for index := range ids {
		ids[index] = batchIssueID1
	}
	_, _, _, _, err := normalizeBatchIssueInput(BatchIssueInput{IDs: ids, Action: "submit", Note: "完成"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestBatchIssueError(t *testing.T) {
	code, message := batchIssueError(ErrInvalidTransition, BatchIssueActionSubmit)
	if code != "invalid_status_transition" || !strings.Contains(message, "提交复核") {
		t.Fatalf("code=%q message=%q", code, message)
	}
	code, _ = batchIssueError(ErrForbidden, BatchIssueActionAssign)
	if code != "forbidden" {
		t.Fatalf("code=%q, want forbidden", code)
	}
}
