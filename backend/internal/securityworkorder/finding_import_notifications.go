package securityworkorder

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"netassets/internal/notification"
)

type findingImportNotificationKey struct {
	eventType, organization, user, batch string
	// Blank request IDs retain WriteEvent's per-row unique fallback behavior.
	unbatched int
}
type findingImportNotification struct {
	key   findingImportNotificationKey
	event notification.EventInput
	count int
}
type findingImportNotifications struct {
	groups map[findingImportNotificationKey]*findingImportNotification
	serial int
}

func newFindingImportNotifications() *findingImportNotifications {
	return &findingImportNotifications{groups: make(map[findingImportNotificationKey]*findingImportNotification)}
}

// Called ONLY after the row savepoint commits. Failed/duplicate rows never add
// items. The enclosing file transaction still owns findings, audits and digests.
func (n *findingImportNotifications) add(event notification.EventInput) {
	key := findingImportNotificationKey{
		eventType: strings.TrimSpace(event.EventType), organization: strings.TrimSpace(event.TargetOrganizationID),
		user: strings.TrimSpace(event.TargetUserID), batch: strings.TrimSpace(event.BatchKey),
	}
	if key.batch == "" {
		n.serial++
		key.unbatched = n.serial
	}
	group := n.groups[key]
	if group == nil {
		group = &findingImportNotification{key: key}
		n.groups[key] = group
	}
	// Finding-create events have a fixed object schema. Match sequential JSONB
	// object || object semantics and last-row severity/details, not max severity.
	details := make(map[string]any)
	for k, v := range group.event.Details {
		details[k] = v
	}
	for k, v := range event.Details {
		details[k] = v
	}
	group.event = event
	group.event.Details = details
	group.count++
}
func (n *findingImportNotifications) flush(ctx context.Context, tx pgx.Tx) error {
	groups := make([]*findingImportNotification, 0, len(n.groups))
	for _, group := range n.groups {
		groups = append(groups, group)
	}
	// Parallel files with reversed row order must acquire digest locks in the
	// same order. Map iteration or first-seen order would introduce a deadlock.
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i].key, groups[j].key
		if a.eventType != b.eventType {
			return a.eventType < b.eventType
		}
		if a.organization != b.organization {
			return a.organization < b.organization
		}
		if a.user != b.user {
			return a.user < b.user
		}
		if a.batch != b.batch {
			return a.batch < b.batch
		}
		return a.unbatched < b.unbatched
	})
	for _, group := range groups {
		if err := notification.WriteCountedEvent(ctx, tx, group.event, group.count); err != nil {
			return err
		}
	}
	return nil
}
