package dataquality

import "testing"

func TestValidateIssueFilter(t *testing.T) {
	tests := []struct {
		name    string
		filter  IssueFilter
		wantErr bool
		limit   int
	}{
		{name: "defaults limit", filter: IssueFilter{}, limit: 100},
		{name: "caps limit", filter: IssueFilter{Limit: 900}, limit: 500},
		{name: "valid filters", filter: IssueFilter{OrganizationID: "550e8400-e29b-41d4-a716-446655440000", Status: "pending_review", Severity: "error", ResourceType: "asset", Assignee: "me", Limit: 20}, limit: 20},
		{name: "invalid organization", filter: IssueFilter{OrganizationID: "not-a-uuid"}, wantErr: true},
		{name: "invalid status", filter: IssueFilter{Status: "resolved"}, wantErr: true},
		{name: "invalid severity", filter: IssueFilter{Severity: "critical"}, wantErr: true},
		{name: "invalid resource type", filter: IssueFilter{ResourceType: "organization"}, wantErr: true},
		{name: "invalid assignee", filter: IssueFilter{Assignee: "someone"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := tt.filter
			err := validateIssueFilter(&filter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateIssueFilter() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && filter.Limit != tt.limit {
				t.Fatalf("limit = %d, want %d", filter.Limit, tt.limit)
			}
		})
	}
}

func TestValidIssueStatus(t *testing.T) {
	for _, status := range []string{"pending", "in_progress", "pending_review", "closed", "exempted"} {
		if !validIssueStatus(status) {
			t.Fatalf("status %q should be valid", status)
		}
	}
	for _, status := range []string{"", "resolved", "deleted"} {
		if validIssueStatus(status) {
			t.Fatalf("status %q should be invalid", status)
		}
	}
}
