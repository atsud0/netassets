package securityworkorder

import (
	"netassets/internal/notification"
	"testing"
)

func TestFindingImportNotificationGrouping(t *testing.T) {
	n := newFindingImportNotifications()
	add := func(org, user, batch, severity string) {
		n.add(notification.EventInput{EventType: notification.EventFindingCreatedOnAsset, TargetOrganizationID: org, TargetUserID: user, BatchKey: batch, Level: severity, Details: map[string]any{"severity": severity}})
	}
	add("org-a", "owner-a", "batch", "danger")
	add("org-a", "owner-a", "batch", "info")
	add("org-a", "owner-b", "batch", "warning")
	add("org-b", "owner-a", "batch", "danger")
	add("org-a", "owner-a", "different-batch", "danger")
	add("org-a", "owner-a", "", "danger")
	add("org-a", "owner-a", "  ", "info")
	if len(n.groups) != 6 {
		t.Fatalf("groups=%d, want 6 (owner/org/batch separation; blank batches never aggregate)", len(n.groups))
	}
	group := n.groups[findingImportNotificationKey{eventType: notification.EventFindingCreatedOnAsset, organization: "org-a", user: "owner-a", batch: "batch"}]
	if group == nil || group.count != 2 || group.event.Level != "info" || group.event.Details["severity"] != "info" {
		t.Fatalf("last successful row's digest semantics lost: %+v", group)
	}
}
