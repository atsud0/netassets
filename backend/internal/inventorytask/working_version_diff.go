package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
)

// WorkingVersionDiff compares one active organization working version with the
// corresponding headquarters-published resource. It deliberately reuses the
// submission snapshot field definitions so pre-submission and review-time
// comparisons have identical labels and value semantics.
type WorkingVersionDiff struct {
	ResourceType   string                  `json:"resource_type"`
	DraftID        string                  `json:"draft_id"`
	SourceID       string                  `json:"source_id,omitempty"`
	OrganizationID string                  `json:"organization_id"`
	TaskID         string                  `json:"task_id,omitempty"`
	Operation      string                  `json:"operation"`
	DraftStatus    string                  `json:"draft_status"`
	ResourceKey    string                  `json:"resource_key"`
	ResourceName   string                  `json:"resource_name"`
	Changes        []SubmissionFieldChange `json:"changes"`
}

type workingVersionSnapshot struct {
	WorkingVersionDiff
	SourceExists bool
	Before       map[string]any
	After        map[string]any
}

// WorkingVersionDiff returns a field-level comparison without widening draft
// visibility. Organization users must hold the resource permission in the
// draft's organization. Headquarters can only inspect task-bound versions that
// have already been submitted for review. Published history is compared through
// immutable submission snapshots; daily unreported work remains private.
func (s *Service) WorkingVersionDiff(ctx context.Context, principal auth.Principal, resourceType, draftID string) (WorkingVersionDiff, error) {
	resourceType = strings.TrimSpace(resourceType)
	draftID = strings.TrimSpace(draftID)
	if !validSubmissionDiffResourceType(resourceType) || draftID == "" {
		return WorkingVersionDiff{}, ErrInvalid
	}

	var (
		snapshot workingVersionSnapshot
		err      error
	)
	if resourceType == "network_asset" {
		if !principal.HasRole("group_admin") && !principal.HasPermission("asset.manage") {
			return WorkingVersionDiff{}, ErrForbidden
		}
		snapshot, err = s.loadAssetWorkingVersionSnapshot(ctx, draftID)
	} else {
		if !principal.HasRole("group_admin") && !principal.HasPermission("system.manage") {
			return WorkingVersionDiff{}, ErrForbidden
		}
		snapshot, err = s.loadBusinessSystemWorkingVersionSnapshot(ctx, draftID)
	}
	if err != nil {
		return WorkingVersionDiff{}, err
	}

	if principal.HasRole("group_admin") {
		if snapshot.TaskID == "" || snapshot.DraftStatus != "submitted" {
			return WorkingVersionDiff{}, ErrForbidden
		}
	} else {
		permission := "asset.manage"
		if resourceType == "business_system" {
			permission = "system.manage"
		}
		allowed, accessErr := access.CanOperateOrganization(ctx, s.pool, principal, snapshot.OrganizationID, permission)
		if accessErr != nil {
			return WorkingVersionDiff{}, accessErr
		}
		if !allowed || (snapshot.TaskID != "" && !principal.HasPermission("inventory.manage")) {
			return WorkingVersionDiff{}, ErrForbidden
		}
	}

	if (snapshot.Operation == "update" || snapshot.Operation == "delete") && !snapshot.SourceExists {
		return WorkingVersionDiff{}, ErrNotFound
	}
	snapshot.Changes = compareSnapshotData(resourceType, snapshot.Before, snapshot.After)
	return snapshot.WorkingVersionDiff, nil
}

func (s *Service) loadAssetWorkingVersionSnapshot(ctx context.Context, draftID string) (workingVersionSnapshot, error) {
	return scanWorkingVersionSnapshot(s.pool.QueryRow(ctx, `
		SELECT 'network_asset', draft.id::text, COALESCE(draft.source_asset_id::text, ''), draft.organization_id::text,
		       COALESCE(draft.task_id::text, ''), draft.operation, draft.draft_status,
		       draft.asset_type || ':' || draft.normalized_value, COALESCE(NULLIF(draft.asset_name, ''), draft.asset_value),
		       (source.id IS NOT NULL),
		       CASE WHEN source.id IS NULL THEN '{}'::jsonb ELSE
		           jsonb_build_object(
		               'asset_type', source.asset_type, 'asset_value', source.asset_value,
		               'asset_name', COALESCE(source.asset_name, ''), 'owner_name', COALESCE(source_owner.owner_name, ''),
		               'environment', source.environment, 'network_scope', source.network_scope,
		               'network_area_name', COALESCE(source_area.area_name, ''), 'status', source.status,
		               'importance', source.importance, 'expires_at', source.expires_at,
		               'linked_system_codes', COALESCE((
		                   SELECT jsonb_agg(system.system_code ORDER BY system.system_code)
		                   FROM system_assets link
		                   JOIN business_systems system ON system.id=link.business_system_id
		                   WHERE link.asset_id=source.id AND system.deleted_at IS NULL
		               ), '[]'::jsonb),
		               'notes', COALESCE(source.notes, '')
		           ) || COALESCE((
		               SELECT jsonb_object_agg('custom:' || definition.field_key, value.value)
		               FROM custom_field_values value
		               JOIN custom_field_definitions definition ON definition.id=value.field_definition_id
		               WHERE value.resource_type='asset' AND value.resource_id=source.id
		           ), '{}'::jsonb) || jsonb_build_object('_custom_field_labels', COALESCE((
		               SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		               FROM custom_field_values value
		               JOIN custom_field_definitions definition ON definition.id=value.field_definition_id
		               WHERE value.resource_type='asset' AND value.resource_id=source.id
		           ), '{}'::jsonb)) END,
		       jsonb_build_object(
		           'asset_type', draft.asset_type, 'asset_value', draft.asset_value,
		           'asset_name', COALESCE(draft.asset_name, ''), 'owner_name', COALESCE(draft.owner_name, ''),
		           'environment', draft.environment, 'network_scope', draft.network_scope,
		           'network_area_name', COALESCE(draft_area.area_name, ''), 'status', draft.status,
		           'importance', draft.importance, 'expires_at', draft.expires_at,
		           'linked_system_codes', CASE
		               WHEN working_system.id IS NOT NULL THEN jsonb_build_array(working_system.system_code)
		               WHEN draft_system.id IS NOT NULL THEN jsonb_build_array(draft_system.system_code)
		               ELSE '[]'::jsonb END,
		           'notes', COALESCE(draft.notes, '')
		       ) || COALESCE((
		           SELECT jsonb_object_agg('custom:' || (value->>'field_key'), value->'value')
		           FROM jsonb_array_elements(draft.custom_values) value
		       ), '{}'::jsonb) || jsonb_build_object('_custom_field_labels', COALESCE((
		           SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		           FROM jsonb_array_elements(draft.custom_values) value
		           JOIN custom_field_definitions definition ON definition.resource_type='asset' AND definition.field_key=value->>'field_key'
		       ), '{}'::jsonb))
		FROM inventory_asset_drafts draft
		LEFT JOIN assets source ON source.id=draft.source_asset_id
		LEFT JOIN asset_organizations source_owner ON source_owner.asset_id=source.id AND source_owner.is_primary
		LEFT JOIN asset_network_areas source_area ON source_area.id=source.network_area_id
		LEFT JOIN asset_network_areas draft_area ON draft_area.id=draft.network_area_id
		LEFT JOIN business_systems draft_system ON draft_system.id=draft.business_system_id AND draft_system.deleted_at IS NULL
		LEFT JOIN inventory_business_system_drafts working_system ON working_system.id=draft.business_system_draft_id AND working_system.draft_status IN ('editing','submitted','returned')
		WHERE draft.id::text=$1 AND draft.draft_status IN ('editing','submitted','returned')
	`, draftID))
}

func (s *Service) loadBusinessSystemWorkingVersionSnapshot(ctx context.Context, draftID string) (workingVersionSnapshot, error) {
	return scanWorkingVersionSnapshot(s.pool.QueryRow(ctx, `
		SELECT 'business_system', draft.id::text, COALESCE(draft.source_system_id::text, ''), draft.organization_id::text,
		       COALESCE(draft.task_id::text, ''), draft.operation, draft.draft_status,
		       draft.system_code, draft.system_name, (source.id IS NOT NULL),
		       CASE WHEN source.id IS NULL THEN '{}'::jsonb ELSE
		           jsonb_build_object(
		               'system_code', source.system_code, 'system_name', source.system_name,
		               'short_name', COALESCE(source.short_name, ''), 'owner_name', COALESCE(source.owner_name, ''),
		               'owner_contact', COALESCE(source.owner_contact, ''), 'system_status', source.system_status,
		               'system_level', source.system_level, 'deployment_type', COALESCE(source.deployment_type, ''),
		               'internet_exposure', source.internet_exposure, 'description', COALESCE(source.description, ''),
		               'contains_personal_information', COALESCE(profile.contains_personal_information, 'unknown'),
		               'contains_sensitive_personal_information', COALESCE(profile.contains_sensitive_personal_information, 'unknown'),
		               'personal_information_record_count', profile.personal_information_record_count,
		               'data_volume_over_100k', COALESCE(profile.data_volume_over_100k, 'unknown'),
		               'contains_important_data', COALESCE(profile.contains_important_data, 'unknown'),
		               'contains_core_data', COALESCE(profile.contains_core_data, 'unknown'),
		               'data_storage_location', COALESCE(profile.data_storage_location, ''),
		               'security_notes', COALESCE(profile.notes, '')
		           ) || COALESCE((
		               SELECT jsonb_object_agg('custom:' || definition.field_key, value.value)
		               FROM custom_field_values value
		               JOIN custom_field_definitions definition ON definition.id=value.field_definition_id
		               WHERE value.resource_type='business_system' AND value.resource_id=source.id
		           ), '{}'::jsonb) || jsonb_build_object('_custom_field_labels', COALESCE((
		               SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		               FROM custom_field_values value
		               JOIN custom_field_definitions definition ON definition.id=value.field_definition_id
		               WHERE value.resource_type='business_system' AND value.resource_id=source.id
		           ), '{}'::jsonb)) END,
		       jsonb_build_object(
		           'system_code', draft.system_code, 'system_name', draft.system_name,
		           'short_name', COALESCE(draft.short_name, ''), 'owner_name', COALESCE(draft.owner_name, ''),
		           'owner_contact', COALESCE(draft.owner_contact, ''), 'system_status', draft.system_status,
		           'system_level', draft.system_level, 'deployment_type', COALESCE(draft.deployment_type, ''),
		           'internet_exposure', draft.internet_exposure, 'description', COALESCE(draft.description, ''),
		           'contains_personal_information', draft.contains_personal_information,
		           'contains_sensitive_personal_information', draft.contains_sensitive_personal_information,
		           'personal_information_record_count', draft.personal_information_record_count,
		           'data_volume_over_100k', draft.data_volume_over_100k,
		           'contains_important_data', draft.contains_important_data,
		           'contains_core_data', draft.contains_core_data,
		           'data_storage_location', COALESCE(draft.data_storage_location, ''),
		           'security_notes', COALESCE(draft.security_notes, '')
		       ) || COALESCE((
		           SELECT jsonb_object_agg('custom:' || (value->>'field_key'), value->'value')
		           FROM jsonb_array_elements(draft.custom_values) value
		       ), '{}'::jsonb) || jsonb_build_object('_custom_field_labels', COALESCE((
		           SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		           FROM jsonb_array_elements(draft.custom_values) value
		           JOIN custom_field_definitions definition ON definition.resource_type='business_system' AND definition.field_key=value->>'field_key'
		       ), '{}'::jsonb))
		FROM inventory_business_system_drafts draft
		LEFT JOIN business_systems source ON source.id=draft.source_system_id
		LEFT JOIN business_system_data_security_profiles profile ON profile.business_system_id=source.id
		WHERE draft.id::text=$1 AND draft.draft_status IN ('editing','submitted','returned')
	`, draftID))
}

func scanWorkingVersionSnapshot(row pgx.Row) (workingVersionSnapshot, error) {
	var (
		item       workingVersionSnapshot
		beforeJSON []byte
		afterJSON  []byte
	)
	err := row.Scan(
		&item.ResourceType, &item.DraftID, &item.SourceID, &item.OrganizationID, &item.TaskID,
		&item.Operation, &item.DraftStatus, &item.ResourceKey, &item.ResourceName,
		&item.SourceExists, &beforeJSON, &afterJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return workingVersionSnapshot{}, ErrNotFound
	}
	if err != nil {
		return workingVersionSnapshot{}, fmt.Errorf("load working version diff: %w", err)
	}
	if err := json.Unmarshal(beforeJSON, &item.Before); err != nil {
		return workingVersionSnapshot{}, fmt.Errorf("decode published resource snapshot: %w", err)
	}
	if err := json.Unmarshal(afterJSON, &item.After); err != nil {
		return workingVersionSnapshot{}, fmt.Errorf("decode working resource snapshot: %w", err)
	}
	return item, nil
}
