package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	CategoryAssetChange           = "asset_change"
	CategoryBusinessSystemChange  = "business_system_change"
	CategoryFindingReceived       = "finding_received"
	CategoryInventoryTaskReview   = "inventory_task_review"
	CategoryInventoryTaskReceived = "inventory_task_received"
	CategoryInventoryTaskReturned = "inventory_task_returned"
	CategoryInventoryTaskDueSoon  = "inventory_task_due_soon"
	CategoryInventoryTaskOverdue  = "inventory_task_overdue"

	EventAssetStatusChanged      = "asset.status_changed"
	EventAssetNetworkAreaChanged = "asset.network_area_changed"
	EventAssetArchived           = "asset.archived"
	EventAssetDeleted            = "asset.deleted"
	EventAssetRestored           = "asset.restored"

	EventBusinessSystemArchived = "business_system.archived"
	EventBusinessSystemRestored = "business_system.restored"
	EventBusinessSystemDeleted  = "business_system.deleted"

	EventFindingCreatedOnAsset  = "security_finding.created_on_asset"
	EventFindingCreatedOnSystem = "security_finding.created_on_system"
)

type eventExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type EventInput struct {
	EventType            string
	Category             string
	TargetOrganizationID string
	ActorUserID          string
	RequiredPermission   string
	ResourceType         string
	ResourceID           string
	ResourceLabel        string
	BatchKey             string
	Level                string
	Details              map[string]any
}

// WriteEvent records a durable organization-scoped notification event. Calls
// sharing event type, organization and request batch key are aggregated so a
// 200-item batch operation produces one notification instead of 200 messages.
func WriteEvent(ctx context.Context, execer eventExecer, input EventInput) error {
	input.EventType = strings.TrimSpace(input.EventType)
	input.Category = strings.TrimSpace(input.Category)
	input.TargetOrganizationID = strings.TrimSpace(input.TargetOrganizationID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.RequiredPermission = strings.TrimSpace(input.RequiredPermission)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.ResourceLabel = strings.TrimSpace(input.ResourceLabel)
	input.BatchKey = strings.TrimSpace(input.BatchKey)
	input.Level = strings.TrimSpace(input.Level)
	if input.BatchKey == "" {
		input.BatchKey = fmt.Sprintf("%s:%d", input.ResourceID, time.Now().UnixNano())
	}
	if input.Level == "" {
		input.Level = "info"
	}
	if input.EventType == "" || input.Category == "" || input.TargetOrganizationID == "" || input.ActorUserID == "" ||
		input.RequiredPermission == "" || input.ResourceType == "" || input.BatchKey == "" {
		return fmt.Errorf("invalid notification event")
	}
	details, err := json.Marshal(input.Details)
	if err != nil {
		return fmt.Errorf("encode notification event details: %w", err)
	}
	_, err = execer.Exec(ctx, `
		INSERT INTO notification_events (
			event_type, category, target_organization_id, actor_user_id, required_permission,
			resource_type, resource_id, resource_label, batch_key, level, details
		) VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, NULLIF($7, ''), $8, $9, $10, $11::jsonb)
		ON CONFLICT (event_type, target_organization_id, batch_key) DO UPDATE SET
			item_count = notification_events.item_count + 1,
			resource_id = NULL,
			resource_label = '',
			level = EXCLUDED.level,
			details = notification_events.details || EXCLUDED.details,
			updated_at = now()
	`, input.EventType, input.Category, input.TargetOrganizationID, input.ActorUserID, input.RequiredPermission,
		input.ResourceType, input.ResourceID, input.ResourceLabel, input.BatchKey, input.Level, details)
	if err != nil {
		return fmt.Errorf("write notification event: %w", err)
	}
	return nil
}
