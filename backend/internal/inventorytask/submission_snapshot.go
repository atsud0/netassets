package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type SubmissionMetadata struct {
	ID                   string    `json:"id"`
	TaskID               string    `json:"task_id"`
	TaskName             string    `json:"task_name"`
	OrganizationID       string    `json:"organization_id"`
	OrganizationName     string    `json:"organization_name"`
	Version              int       `json:"version"`
	SubmittedAt          time.Time `json:"submitted_at"`
	SubmittedByName      string    `json:"submitted_by_name,omitempty"`
	Note                 string    `json:"note,omitempty"`
	SystemCount          int64     `json:"system_count"`
	AssetCount           int64     `json:"asset_count"`
	PreviousSubmissionID string    `json:"previous_submission_id,omitempty"`
}

type SubmissionFieldChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type SubmissionResourceChange struct {
	ResourceID   string                  `json:"resource_id"`
	ResourceKey  string                  `json:"resource_key"`
	ResourceName string                  `json:"resource_name"`
	Operation    string                  `json:"operation,omitempty"`
	Changes      []SubmissionFieldChange `json:"changes,omitempty"`
}

type SubmissionResourceDiff struct {
	Added    []SubmissionResourceChange `json:"added"`
	Modified []SubmissionResourceChange `json:"modified"`
	Removed  []SubmissionResourceChange `json:"removed"`
}

type SubmissionDiffSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

type SubmissionDiff struct {
	Submission      SubmissionMetadata     `json:"submission"`
	Previous        *SubmissionMetadata    `json:"previous_submission,omitempty"`
	BusinessSystems SubmissionResourceDiff `json:"business_systems"`
	NetworkAssets   SubmissionResourceDiff `json:"network_assets"`
	Summary         SubmissionDiffSummary  `json:"summary"`
}

// SubmissionDiffOverview keeps the initial drawer response bounded. Detailed
// changes are retrieved separately through SearchSubmissionDiffItems.
type SubmissionDiffOverview struct {
	Submission      SubmissionMetadata    `json:"submission"`
	Previous        *SubmissionMetadata   `json:"previous_submission,omitempty"`
	BusinessSystems SubmissionDiffSummary `json:"business_systems"`
	NetworkAssets   SubmissionDiffSummary `json:"network_assets"`
	Summary         SubmissionDiffSummary `json:"summary"`
}

type snapshotItem struct {
	ResourceType string
	ResourceID   string
	ResourceKey  string
	ResourceName string
	Data         map[string]any
}

type snapshotField struct {
	Name  string
	Label string
}

var snapshotFields = map[string][]snapshotField{
	"business_system": {
		{Name: "system_code", Label: "系统编码"},
		{Name: "system_name", Label: "系统名称"},
		{Name: "short_name", Label: "系统简称"},
		{Name: "owner_name", Label: "负责人"},
		{Name: "owner_contact", Label: "联系方式"},
		{Name: "system_status", Label: "运行状态"},
		{Name: "system_level", Label: "系统等级"},
		{Name: "deployment_type", Label: "部署方式"},
		{Name: "internet_exposure", Label: "互联网暴露"},
		{Name: "description", Label: "系统说明"},
		{Name: "contains_personal_information", Label: "包含个人信息"},
		{Name: "contains_sensitive_personal_information", Label: "包含敏感个人信息"},
		{Name: "personal_information_record_count", Label: "个人信息记录数"},
		{Name: "data_volume_over_100k", Label: "是否超过 10 万条"},
		{Name: "contains_important_data", Label: "包含重要数据"},
		{Name: "contains_core_data", Label: "包含核心数据"},
		{Name: "data_storage_location", Label: "数据存储位置"},
		{Name: "security_notes", Label: "数据安全备注"},
	},
	"network_asset": {
		{Name: "asset_type", Label: "资产类型"},
		{Name: "asset_value", Label: "资产值"},
		{Name: "asset_name", Label: "资产名称"},
		{Name: "owner_name", Label: "负责人"},
		{Name: "environment", Label: "环境"},
		{Name: "network_scope", Label: "网络暴露范围"},
		{Name: "network_area_name", Label: "网络区域"},
		{Name: "status", Label: "状态"},
		{Name: "importance", Label: "重要程度"},
		{Name: "expires_at", Label: "有效期至"},
		{Name: "linked_system_codes", Label: "关联业务系统"},
		{Name: "notes", Label: "备注"},
	},
}

func (s *Service) SubmissionDiff(ctx context.Context, principal auth.Principal, taskID, organizationID string) (SubmissionDiffOverview, error) {
	current, previous, err := s.loadSubmissionMetadata(ctx, principal, taskID, organizationID)
	if err != nil {
		return SubmissionDiffOverview{}, err
	}
	result := SubmissionDiffOverview{Submission: current, Previous: previous}
	for _, resourceType := range []string{"business_system", "network_asset"} {
		summary, err := s.submissionDiffSummary(ctx, current.ID, submissionID(previous), resourceType)
		if err != nil {
			return SubmissionDiffOverview{}, err
		}
		if resourceType == "business_system" {
			result.BusinessSystems = summary
		} else {
			result.NetworkAssets = summary
		}
		result.Summary.Added += summary.Added
		result.Summary.Modified += summary.Modified
		result.Summary.Removed += summary.Removed
	}
	return result, nil
}

// SearchSubmissionDiffItems returns one bounded resource/change collection.
// Modified rows are streamed so comparison retains the existing normalized
// JSON semantics without retaining every changed resource in memory.
func (s *Service) SearchSubmissionDiffItems(
	ctx context.Context,
	principal auth.Principal,
	taskID, organizationID, resourceType, changeType string,
	page pagination.Query,
) (pagination.Result[SubmissionResourceChange], error) {
	resourceType = strings.TrimSpace(resourceType)
	changeType = strings.TrimSpace(changeType)
	if !validSubmissionDiffResourceType(resourceType) || !validSubmissionDiffChangeType(changeType) {
		return pagination.Result[SubmissionResourceChange]{}, ErrInvalid
	}
	current, previous, err := s.loadSubmissionMetadata(ctx, principal, taskID, organizationID)
	if err != nil {
		return pagination.Result[SubmissionResourceChange]{}, err
	}
	previousID := submissionID(previous)
	if changeType == "modified" {
		return s.searchModifiedSubmissionDiffItems(ctx, current.ID, previousID, resourceType, page)
	}
	return s.searchSimpleSubmissionDiffItems(ctx, current.ID, previousID, resourceType, changeType, page)
}

func (s *Service) submissionDiffSummary(ctx context.Context, currentID, previousID, resourceType string) (SubmissionDiffSummary, error) {
	added, err := s.countSimpleSubmissionDiffItems(ctx, currentID, previousID, resourceType, "added")
	if err != nil {
		return SubmissionDiffSummary{}, err
	}
	removed, err := s.countSimpleSubmissionDiffItems(ctx, currentID, previousID, resourceType, "removed")
	if err != nil {
		return SubmissionDiffSummary{}, err
	}
	modified, err := s.countModifiedSubmissionDiffItems(ctx, currentID, previousID, resourceType)
	if err != nil {
		return SubmissionDiffSummary{}, err
	}
	return SubmissionDiffSummary{Added: added, Modified: modified, Removed: removed}, nil
}

func submissionID(metadata *SubmissionMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.ID
}

func validSubmissionDiffResourceType(value string) bool {
	return value == "business_system" || value == "network_asset"
}

func validSubmissionDiffChangeType(value string) bool {
	return value == "added" || value == "modified" || value == "removed"
}

func (s *Service) countSimpleSubmissionDiffItems(ctx context.Context, currentID, previousID, resourceType, changeType string) (int, error) {
	result, err := s.searchSimpleSubmissionDiffItems(ctx, currentID, previousID, resourceType, changeType, pagination.Query{Page: 1, PageSize: 1})
	if err != nil {
		return 0, err
	}
	return result.Total, nil
}

func (s *Service) searchSimpleSubmissionDiffItems(
	ctx context.Context,
	currentID, previousID, resourceType, changeType string,
	page pagination.Query,
) (pagination.Result[SubmissionResourceChange], error) {
	if changeType != "added" && changeType != "removed" {
		return pagination.Result[SubmissionResourceChange]{}, ErrInvalid
	}

	// Draft operations are authoritative for the current task submission. An
	// update/delete draft has a different draft UUID from its formal resource,
	// so relying on UUID comparison alone would incorrectly render it as one
	// added row plus one removed row.
	query := `
		WITH candidates AS (
			SELECT current_item.resource_id, current_item.resource_key, current_item.resource_name,
			       COALESCE(current_item.snapshot_data->>'_inventory_operation', '') AS operation
			FROM inventory_submission_items current_item
			WHERE current_item.submission_id = $1::uuid
			  AND current_item.resource_type = $3
			  AND (
			      ($4 = 'added' AND (
			          current_item.snapshot_data->>'_inventory_operation' = 'create'
			          OR (
			              COALESCE(current_item.snapshot_data->>'_inventory_operation', '') NOT IN ('update', 'delete')
			              AND ($2::text = '' OR NOT EXISTS (
			                  SELECT 1 FROM inventory_submission_items previous_item
			                  WHERE previous_item.submission_id = NULLIF($2, '')::uuid
			                    AND previous_item.resource_type = current_item.resource_type
			                    AND previous_item.resource_id = current_item.resource_id
			              ))
			          )
			      ))
			      OR ($4 = 'removed' AND current_item.snapshot_data->>'_inventory_operation' = 'delete')
			  )
			UNION ALL
			SELECT previous_item.resource_id, previous_item.resource_key, previous_item.resource_name, '' AS operation
			FROM inventory_submission_items previous_item
			WHERE $4 = 'removed'
			  AND $2::text <> ''
			  AND previous_item.submission_id = NULLIF($2, '')::uuid
			  AND previous_item.resource_type = $3
			  AND NOT EXISTS (
			      SELECT 1 FROM inventory_submission_items current_item
			      WHERE current_item.submission_id = $1::uuid
			        AND current_item.resource_type = previous_item.resource_type
			        AND current_item.resource_id = previous_item.resource_id
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM inventory_submission_items current_change
			      WHERE current_change.submission_id = $1::uuid
			        AND current_change.resource_type = previous_item.resource_type
			        AND current_change.snapshot_data->>'_inventory_operation' IN ('update', 'delete')
			        AND current_change.snapshot_data->>'_source_resource_id' = previous_item.resource_id::text
			  )
		)
		SELECT resource_id::text, resource_key, resource_name, operation, count(*) OVER()
		FROM candidates
		ORDER BY resource_key, resource_id
		LIMIT $5 OFFSET $6
	`
	rows, err := s.pool.Query(ctx, query, currentID, previousID, resourceType, changeType, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("list inventory submission %s items: %w", changeType, err)
	}
	defer rows.Close()
	items := make([]SubmissionResourceChange, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item SubmissionResourceChange
		if err := rows.Scan(&item.ResourceID, &item.ResourceKey, &item.ResourceName, &item.Operation, &total); err != nil {
			return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("scan inventory submission %s item: %w", changeType, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("iterate inventory submission %s items: %w", changeType, err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) countModifiedSubmissionDiffItems(ctx context.Context, currentID, previousID, resourceType string) (int, error) {
	result, err := s.searchModifiedSubmissionDiffItems(ctx, currentID, previousID, resourceType, pagination.Query{Page: 1, PageSize: 1})
	if err != nil {
		return 0, err
	}
	return result.Total, nil
}

func (s *Service) searchModifiedSubmissionDiffItems(
	ctx context.Context,
	currentID, previousID, resourceType string,
	page pagination.Query,
) (pagination.Result[SubmissionResourceChange], error) {
	rows, err := s.pool.Query(ctx, `
		SELECT current_item.resource_id::text, current_item.resource_key, current_item.resource_name,
		       COALESCE(current_item.snapshot_data->>'_inventory_operation', ''),
		       previous_item.snapshot_data, current_item.snapshot_data
		FROM inventory_submission_items current_item
		LEFT JOIN inventory_submission_items previous_item
		  ON $2::text <> ''
		 AND previous_item.submission_id = NULLIF($2, '')::uuid
		 AND previous_item.resource_type = current_item.resource_type
		 AND previous_item.resource_id = CASE
		     WHEN current_item.snapshot_data->>'_inventory_operation' = 'update'
		       AND COALESCE(current_item.snapshot_data->>'_source_resource_id', '') <> ''
		     THEN (current_item.snapshot_data->>'_source_resource_id')::uuid
		     ELSE current_item.resource_id
		 END
		WHERE current_item.submission_id = $1::uuid
		  AND current_item.resource_type = $3
		  AND (
		      current_item.snapshot_data->>'_inventory_operation' = 'update'
		      OR (
		          COALESCE(current_item.snapshot_data->>'_inventory_operation', '') NOT IN ('create', 'delete')
		          AND previous_item.resource_id IS NOT NULL
		      )
		  )
		ORDER BY current_item.resource_key, current_item.resource_id
	`, currentID, previousID, resourceType)
	if err != nil {
		return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("list inventory submission modified candidates: %w", err)
	}
	defer rows.Close()

	items := make([]SubmissionResourceChange, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item SubmissionResourceChange
		var beforeData, afterData []byte
		if err := rows.Scan(&item.ResourceID, &item.ResourceKey, &item.ResourceName, &item.Operation, &beforeData, &afterData); err != nil {
			return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("scan inventory submission modified candidate: %w", err)
		}
		var before, after map[string]any
		if len(beforeData) > 0 {
			if err := json.Unmarshal(beforeData, &before); err != nil {
				return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("decode previous inventory submission item: %w", err)
			}
		}
		if err := json.Unmarshal(afterData, &after); err != nil {
			return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("decode current inventory submission item: %w", err)
		}
		if item.Operation == "update" {
			if embeddedBefore, ok := after[inventoryBeforeKey].(map[string]any); ok {
				before = embeddedBefore
			}
		}
		if before == nil {
			before = map[string]any{}
		}
		changes := compareSnapshotData(resourceType, before, after)
		if len(changes) == 0 {
			continue
		}
		if total >= page.Offset() && len(items) < page.PageSize {
			item.Changes = changes
			items = append(items, item)
		}
		total++
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[SubmissionResourceChange]{}, fmt.Errorf("iterate inventory submission modified candidates: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) loadSubmissionMetadata(ctx context.Context, principal auth.Principal, taskID, organizationID string) (SubmissionMetadata, *SubmissionMetadata, error) {
	var current SubmissionMetadata
	var previousID *string
	err := s.pool.QueryRow(ctx, contributorUpdatePrefix+`
		SELECT submission.id::text, submission.task_id::text, task.task_name,
		       submission.organization_id::text, organization.organization_name,
		       submission.submission_version, submission.submitted_at,
		       COALESCE(submitter.display_name, ''), COALESCE(submission.submission_note, ''),
		       submission.system_count, submission.asset_count, submission.previous_submission_id::text
		FROM inventory_task_organizations progress
		JOIN inventory_tasks task ON task.id = progress.task_id
		JOIN inventory_submissions submission ON submission.id = progress.latest_submission_id
		JOIN organizations organization ON organization.id = submission.organization_id
		LEFT JOIN users submitter ON submitter.id = submission.submitted_by
		WHERE progress.task_id::text = $2 AND progress.organization_id::text = $3
		  AND ($4::boolean OR progress.organization_id IN (SELECT id FROM accessible))
	`, principal.UserID, taskID, organizationID, principal.HasRole("group_admin")).Scan(
		&current.ID, &current.TaskID, &current.TaskName, &current.OrganizationID, &current.OrganizationName,
		&current.Version, &current.SubmittedAt, &current.SubmittedByName, &current.Note,
		&current.SystemCount, &current.AssetCount, &previousID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmissionMetadata{}, nil, ErrNotFound
	}
	if err != nil {
		return SubmissionMetadata{}, nil, fmt.Errorf("load current inventory submission: %w", err)
	}
	if previousID == nil || *previousID == "" {
		return current, nil, nil
	}
	current.PreviousSubmissionID = *previousID
	var previous SubmissionMetadata
	err = s.pool.QueryRow(ctx, `
		SELECT submission.id::text, submission.task_id::text, task.task_name,
		       submission.organization_id::text, organization.organization_name,
		       submission.submission_version, submission.submitted_at,
		       COALESCE(submitter.display_name, ''), COALESCE(submission.submission_note, ''),
		       submission.system_count, submission.asset_count,
		       COALESCE(submission.previous_submission_id::text, '')
		FROM inventory_submissions submission
		JOIN inventory_tasks task ON task.id = submission.task_id
		JOIN organizations organization ON organization.id = submission.organization_id
		LEFT JOIN users submitter ON submitter.id = submission.submitted_by
		WHERE submission.id = $1::uuid
	`, *previousID).Scan(
		&previous.ID, &previous.TaskID, &previous.TaskName, &previous.OrganizationID, &previous.OrganizationName,
		&previous.Version, &previous.SubmittedAt, &previous.SubmittedByName, &previous.Note,
		&previous.SystemCount, &previous.AssetCount, &previous.PreviousSubmissionID,
	)
	if err != nil {
		return SubmissionMetadata{}, nil, fmt.Errorf("load previous inventory submission: %w", err)
	}
	return current, &previous, nil
}

type snapshotQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadSnapshotItems(ctx context.Context, q snapshotQueryer, submissionID string) ([]snapshotItem, error) {
	rows, err := q.Query(ctx, `
		SELECT resource_type, resource_id::text, resource_key, resource_name, snapshot_data
		FROM inventory_submission_items
		WHERE submission_id = $1::uuid
		ORDER BY resource_type, resource_key, resource_id
	`, submissionID)
	if err != nil {
		return nil, fmt.Errorf("load inventory submission items: %w", err)
	}
	defer rows.Close()
	items := make([]snapshotItem, 0)
	for rows.Next() {
		var item snapshotItem
		var data []byte
		if err := rows.Scan(&item.ResourceType, &item.ResourceID, &item.ResourceKey, &item.ResourceName, &data); err != nil {
			return nil, fmt.Errorf("scan inventory submission item: %w", err)
		}
		if err := json.Unmarshal(data, &item.Data); err != nil {
			return nil, fmt.Errorf("decode inventory submission item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory submission items: %w", err)
	}
	return items, nil
}

func buildSubmissionDiff(currentItems, previousItems []snapshotItem) SubmissionDiff {
	result := SubmissionDiff{
		BusinessSystems: emptyResourceDiff(),
		NetworkAssets:   emptyResourceDiff(),
	}
	for _, resourceType := range []string{"business_system", "network_asset"} {
		current := itemsByID(currentItems, resourceType)
		previous := itemsByID(previousItems, resourceType)
		diff := emptyResourceDiff()
		claimedPrevious := make(map[string]struct{})
		for _, item := range current {
			operation := snapshotString(item.Data, inventoryOperationKey)
			sourceID := snapshotString(item.Data, inventorySourceIDKey)
			if sourceID != "" && (operation == "update" || operation == "delete") {
				claimedPrevious[sourceID] = struct{}{}
			}
			switch operation {
			case "create":
				diff.Added = append(diff.Added, resourceChange(item, operation, nil))
				continue
			case "update":
				before := snapshotMap(item.Data, inventoryBeforeKey)
				if len(before) == 0 {
					if old, exists := previous[sourceID]; exists {
						before = old.Data
					}
				}
				changes := compareSnapshotData(resourceType, before, item.Data)
				if len(changes) > 0 {
					diff.Modified = append(diff.Modified, resourceChange(item, operation, changes))
				}
				continue
			case "delete":
				diff.Removed = append(diff.Removed, resourceChange(item, operation, nil))
				continue
			}
			old, exists := previous[item.ResourceID]
			if !exists {
				diff.Added = append(diff.Added, resourceChange(item, "", nil))
				continue
			}
			changes := compareSnapshotData(resourceType, old.Data, item.Data)
			if len(changes) > 0 {
				diff.Modified = append(diff.Modified, resourceChange(item, "", changes))
			}
		}
		for id, item := range previous {
			if _, claimed := claimedPrevious[id]; claimed {
				continue
			}
			if _, exists := current[id]; !exists {
				diff.Removed = append(diff.Removed, resourceChange(item, "", nil))
			}
		}
		sortResourceChanges(diff.Added)
		sortResourceChanges(diff.Modified)
		sortResourceChanges(diff.Removed)
		if resourceType == "business_system" {
			result.BusinessSystems = diff
		} else {
			result.NetworkAssets = diff
		}
		result.Summary.Added += len(diff.Added)
		result.Summary.Modified += len(diff.Modified)
		result.Summary.Removed += len(diff.Removed)
	}
	return result
}

func emptyResourceDiff() SubmissionResourceDiff {
	return SubmissionResourceDiff{
		Added:    make([]SubmissionResourceChange, 0),
		Modified: make([]SubmissionResourceChange, 0),
		Removed:  make([]SubmissionResourceChange, 0),
	}
}

func itemsByID(items []snapshotItem, resourceType string) map[string]snapshotItem {
	result := make(map[string]snapshotItem)
	for _, item := range items {
		if item.ResourceType == resourceType {
			result[item.ResourceID] = item
		}
	}
	return result
}

func resourceChange(item snapshotItem, operation string, changes []SubmissionFieldChange) SubmissionResourceChange {
	return SubmissionResourceChange{
		ResourceID: item.ResourceID, ResourceKey: item.ResourceKey, ResourceName: item.ResourceName,
		Operation: operation, Changes: changes,
	}
}

func snapshotString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func snapshotMap(data map[string]any, key string) map[string]any {
	value, _ := data[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

const (
	customFieldPrefix     = "custom:"
	customFieldLabelsKey  = "_custom_field_labels"
	inventoryOperationKey = "_inventory_operation"
	inventorySourceIDKey  = "_source_resource_id"
	inventoryBeforeKey    = "_inventory_before"
)

func compareSnapshotData(resourceType string, before, after map[string]any) []SubmissionFieldChange {
	changes := make([]SubmissionFieldChange, 0)
	for _, field := range snapshotFieldsForDiff(resourceType, before, after) {
		beforeValue := normalizeSnapshotValue(before[field.Name])
		afterValue := normalizeSnapshotValue(after[field.Name])
		if reflect.DeepEqual(beforeValue, afterValue) {
			continue
		}
		changes = append(changes, SubmissionFieldChange{
			Field: field.Name, Label: field.Label, Before: beforeValue, After: afterValue,
		})
	}
	return changes
}

// snapshotFieldsForDiff keeps the built-in field order stable and adds custom
// fields saved in either snapshot. Custom labels are retained in the snapshot
// so later field renames or deactivation do not change historical semantics.
func snapshotFieldsForDiff(resourceType string, before, after map[string]any) []snapshotField {
	fields := append([]snapshotField(nil), snapshotFields[resourceType]...)
	keys := map[string]struct{}{}
	for key := range before {
		if strings.HasPrefix(key, customFieldPrefix) {
			keys[key] = struct{}{}
		}
	}
	for key := range after {
		if strings.HasPrefix(key, customFieldPrefix) {
			keys[key] = struct{}{}
		}
	}
	customKeys := make([]string, 0, len(keys))
	for key := range keys {
		customKeys = append(customKeys, key)
	}
	sort.Strings(customKeys)
	for _, key := range customKeys {
		fields = append(fields, snapshotField{Name: key, Label: customFieldLabel(key, after, before)})
	}
	return fields
}

func customFieldLabel(key string, snapshots ...map[string]any) string {
	for _, snapshot := range snapshots {
		labels, ok := snapshot[customFieldLabelsKey].(map[string]any)
		if !ok {
			continue
		}
		if label, ok := labels[key].(string); ok && strings.TrimSpace(label) != "" {
			return "自定义字段：" + strings.TrimSpace(label)
		}
	}
	return "自定义字段：" + strings.TrimPrefix(key, customFieldPrefix)
}

func normalizeSnapshotValue(value any) any {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if values, ok := value.([]any); ok {
		normalized := make([]any, len(values))
		for i := range values {
			normalized[i] = normalizeSnapshotValue(values[i])
		}
		// Multi-select custom fields are sets. Sorting also makes their
		// comparison independent from the order in which a user selected items.
		sort.SliceStable(normalized, func(i, j int) bool {
			left, _ := json.Marshal(normalized[i])
			right, _ := json.Marshal(normalized[j])
			return string(left) < string(right)
		})
		return normalized
	}
	if value == nil {
		return ""
	}
	return value
}

func sortResourceChanges(items []SubmissionResourceChange) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].ResourceKey == items[j].ResourceKey {
			return items[i].ResourceID < items[j].ResourceID
		}
		return items[i].ResourceKey < items[j].ResourceKey
	})
}

func createSubmissionSnapshot(ctx context.Context, tx pgx.Tx, principal auth.Principal, taskID, organizationID, note string) (SubmissionMetadata, error) {
	var previousID *string
	err := tx.QueryRow(ctx, `
		SELECT submission.id::text
		FROM inventory_submissions submission
		JOIN inventory_tasks previous_task ON previous_task.id = submission.task_id
		JOIN inventory_tasks current_task ON current_task.id = $2::uuid
		WHERE submission.organization_id = $1::uuid
		  AND ((previous_task.required_fields ? 'business_systems' OR previous_task.required_fields ? 'data_security') =
		       (current_task.required_fields ? 'business_systems' OR current_task.required_fields ? 'data_security'))
		  AND ((previous_task.required_fields ? 'network_assets') = (current_task.required_fields ? 'network_assets'))
		  AND (NOT (current_task.required_fields ? 'network_assets') OR previous_task.asset_network_scope = current_task.asset_network_scope)
		ORDER BY submission.submitted_at DESC, submission.id DESC
		LIMIT 1
	`, organizationID, taskID).Scan(&previousID)
	if errors.Is(err, pgx.ErrNoRows) {
		previousID = nil
	} else if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("load previous inventory submission: %w", err)
	}

	var item SubmissionMetadata
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_submissions (
			task_id, organization_id, submission_version, previous_submission_id,
			submitted_by, submission_note
		)
		VALUES (
			$1::uuid, $2::uuid,
			(SELECT COALESCE(max(submission_version), 0) + 1 FROM inventory_submissions WHERE task_id = $1::uuid AND organization_id = $2::uuid),
			NULLIF($3, '')::uuid, $4::uuid, NULLIF($5, '')
		)
		RETURNING id::text, task_id::text, organization_id::text, submission_version, submitted_at,
		          COALESCE(submission_note, ''), COALESCE(previous_submission_id::text, '')
	`, taskID, organizationID, stringValue(previousID), principal.UserID, note).Scan(
		&item.ID, &item.TaskID, &item.OrganizationID, &item.Version, &item.SubmittedAt, &item.Note, &item.PreviousSubmissionID,
	)
	if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("create inventory submission: %w", err)
	}

	systemResult, err := tx.Exec(ctx, `
		INSERT INTO inventory_submission_items (
			submission_id, resource_type, resource_id, resource_key, resource_name, snapshot_data
		)
		SELECT $1::uuid, 'business_system', system.id, system.system_code, system.system_name,
		       jsonb_build_object(
			   'system_code', system.system_code,
			   'system_name', system.system_name,
			   'short_name', COALESCE(system.short_name, ''),
			   'owner_name', COALESCE(system.owner_name, ''),
			   'owner_contact', COALESCE(system.owner_contact, ''),
			   'system_status', system.system_status,
			   'system_level', system.system_level,
			   'deployment_type', COALESCE(system.deployment_type, ''),
			   'internet_exposure', system.internet_exposure,
			   'description', COALESCE(system.description, ''),
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
		       JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		       WHERE value.resource_type = 'business_system' AND value.resource_id = system.id
		   ), '{}'::jsonb) || jsonb_build_object(
		       '_custom_field_labels', COALESCE((
		           SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		           FROM custom_field_values value
		           JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		           WHERE value.resource_type = 'business_system' AND value.resource_id = system.id
		       ), '{}'::jsonb)
		   )
		FROM business_systems system
		LEFT JOIN business_system_data_security_profiles profile ON profile.business_system_id = system.id
		WHERE system.owner_organization_id = $2::uuid AND system.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM inventory_business_system_drafts change_draft WHERE change_draft.task_id=$3::uuid AND change_draft.organization_id=$2::uuid AND change_draft.source_system_id=system.id AND change_draft.draft_status IN ('editing','returned'))
		  AND EXISTS (
		      SELECT 1 FROM inventory_tasks task
		      WHERE task.id = $3::uuid
		        AND (task.required_fields ? 'business_systems' OR task.required_fields ? 'data_security')
		  )
	`, item.ID, organizationID, taskID)
	if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("snapshot business systems: %w", err)
	}
	draftSystemResult, err := tx.Exec(ctx, `
		INSERT INTO inventory_submission_items (submission_id,resource_type,resource_id,resource_key,resource_name,snapshot_data)
		SELECT $1::uuid,'business_system',draft.id,draft.system_code,draft.system_name,
		       jsonb_build_object(
		           'system_code',draft.system_code,'system_name',draft.system_name,'short_name',COALESCE(draft.short_name,''),
		           'owner_name',COALESCE(draft.owner_name,''),'owner_contact',COALESCE(draft.owner_contact,''),
		           'system_status',draft.system_status,'system_level',draft.system_level,'deployment_type',COALESCE(draft.deployment_type,''),
		           'internet_exposure',draft.internet_exposure,'description',COALESCE(draft.description,''),
		           'contains_personal_information',draft.contains_personal_information,
		           'contains_sensitive_personal_information',draft.contains_sensitive_personal_information,
		           'personal_information_record_count',draft.personal_information_record_count,
		           'data_volume_over_100k',draft.data_volume_over_100k,'contains_important_data',draft.contains_important_data,
		           'contains_core_data',draft.contains_core_data,'data_storage_location',COALESCE(draft.data_storage_location,''),
		           'security_notes',COALESCE(draft.security_notes,''),'_inventory_draft',true,
		           '_inventory_operation',draft.operation,'_source_resource_id',COALESCE(draft.source_system_id::text,'')
		       ) || COALESCE((
		           SELECT jsonb_object_agg('custom:' || (value->>'field_key'), value->'value')
		           FROM jsonb_array_elements(draft.custom_values) value
		       ),'{}'::jsonb) || jsonb_build_object('_custom_field_labels',COALESCE((
		           SELECT jsonb_object_agg('custom:' || definition.field_key,definition.field_name)
		           FROM jsonb_array_elements(draft.custom_values) value
		           JOIN custom_field_definitions definition ON definition.field_key=value->>'field_key'
		             AND definition.resource_type='business_system'
		       ),'{}'::jsonb))
		FROM inventory_business_system_drafts draft
		WHERE draft.task_id=$3::uuid AND draft.organization_id=$2::uuid
		  AND draft.draft_status IN ('editing','returned')
		  AND EXISTS (SELECT 1 FROM inventory_tasks task WHERE task.id=$3::uuid
		      AND (task.required_fields ? 'business_systems' OR task.required_fields ? 'data_security'))
	`, item.ID, organizationID, taskID)
	if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("snapshot business system drafts: %w", err)
	}
	assetResult, err := tx.Exec(ctx, `
		INSERT INTO inventory_submission_items (
			submission_id, resource_type, resource_id, resource_key, resource_name, snapshot_data
		)
		SELECT $1::uuid, 'network_asset', asset.id,
		       asset.asset_type || ':' || asset.normalized_value,
		       COALESCE(NULLIF(asset.asset_name, ''), asset.asset_value),
		       jsonb_build_object(
			   'asset_type', asset.asset_type,
			   'asset_value', asset.asset_value,
			   'asset_name', COALESCE(asset.asset_name, ''),
			   'owner_name', COALESCE(owner.owner_name, ''),
			   'environment', asset.environment,
			   'network_scope', asset.network_scope,
			   'network_area_name', COALESCE(area.area_name, ''),
			   'status', asset.status,
			   'importance', asset.importance,
			   'expires_at', asset.expires_at,
			   'linked_system_codes', COALESCE((
			       SELECT jsonb_agg(system.system_code ORDER BY system.system_code)
			       FROM system_assets link
			       JOIN business_systems system ON system.id = link.business_system_id
			       WHERE link.asset_id = asset.id AND system.deleted_at IS NULL
			   ), '[]'::jsonb),
			   'notes', COALESCE(asset.notes, '')
		   ) || COALESCE((
		       SELECT jsonb_object_agg('custom:' || definition.field_key, value.value)
		       FROM custom_field_values value
		       JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		       WHERE value.resource_type = 'asset' AND value.resource_id = asset.id
		   ), '{}'::jsonb) || jsonb_build_object(
		       '_custom_field_labels', COALESCE((
		           SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		           FROM custom_field_values value
		           JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		           WHERE value.resource_type = 'asset' AND value.resource_id = asset.id
		       ), '{}'::jsonb)
		   )
		FROM assets asset
		JOIN asset_organizations owner ON owner.asset_id = asset.id AND owner.is_primary
		LEFT JOIN asset_network_areas area ON area.id = asset.network_area_id
		WHERE owner.organization_id = $2::uuid AND asset.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts change_draft WHERE change_draft.task_id=$3::uuid AND change_draft.organization_id=$2::uuid AND change_draft.source_asset_id=asset.id AND change_draft.draft_status IN ('editing','returned'))
		  AND EXISTS (
		      SELECT 1 FROM inventory_tasks task
		      WHERE task.id = $3::uuid
		        AND task.required_fields ? 'network_assets'
		        AND (task.asset_network_scope = 'all' OR asset.network_scope = task.asset_network_scope)
		  )
	`, item.ID, organizationID, taskID)
	if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("snapshot network assets: %w", err)
	}
	draftAssetResult, err := tx.Exec(ctx, `
		INSERT INTO inventory_submission_items (
			submission_id, resource_type, resource_id, resource_key, resource_name, snapshot_data
		)
		SELECT $1::uuid, 'network_asset', draft.id,
		       draft.asset_type || ':' || draft.normalized_value,
		       COALESCE(NULLIF(draft.asset_name, ''), draft.asset_value),
		       jsonb_build_object(
		           'asset_type', draft.asset_type,
		           'asset_value', draft.asset_value,
		           'asset_name', COALESCE(draft.asset_name, ''),
		           'owner_name', COALESCE(draft.owner_name, ''),
		           'environment', draft.environment,
		           'network_scope', draft.network_scope,
		           'network_area_name', COALESCE(area.area_name, ''),
		           'status', draft.status,
		           'importance', draft.importance,
		           'expires_at', draft.expires_at,
		           'linked_system_codes', CASE WHEN system.id IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(system.system_code) END,
		           'notes', COALESCE(draft.notes, ''),
		           '_inventory_draft', true,
		           '_inventory_operation', draft.operation,
		           '_source_resource_id', COALESCE(draft.source_asset_id::text, '')
		       )
		FROM inventory_asset_drafts draft
		LEFT JOIN asset_network_areas area ON area.id=draft.network_area_id
		LEFT JOIN business_systems system ON system.id=draft.business_system_id AND system.deleted_at IS NULL
		WHERE draft.task_id=$3::uuid AND draft.organization_id=$2::uuid
		  AND draft.draft_status IN ('editing','returned')
		  AND EXISTS (
		      SELECT 1 FROM inventory_tasks task
		      WHERE task.id=$3::uuid AND task.required_fields ? 'network_assets'
		        AND (task.asset_network_scope='all' OR draft.network_scope=task.asset_network_scope)
		  )
	`, item.ID, organizationID, taskID)
	if err != nil {
		return SubmissionMetadata{}, fmt.Errorf("snapshot network asset drafts: %w", err)
	}
	if err := attachSubmissionDraftBeforeSnapshots(ctx, tx, item.ID, taskID, organizationID); err != nil {
		return SubmissionMetadata{}, err
	}
	item.SystemCount = systemResult.RowsAffected() + draftSystemResult.RowsAffected()
	item.AssetCount = assetResult.RowsAffected() + draftAssetResult.RowsAffected()
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_submissions
		SET system_count = $2, asset_count = $3
		WHERE id = $1::uuid
	`, item.ID, item.SystemCount, item.AssetCount); err != nil {
		return SubmissionMetadata{}, fmt.Errorf("update inventory submission counts: %w", err)
	}
	return item, nil
}

// attachSubmissionDraftBeforeSnapshots freezes the formal resource state that
// existed at submit time. Reviewers can therefore see an accurate before/after
// comparison even after approval publishes the draft into the formal ledger.
func attachSubmissionDraftBeforeSnapshots(ctx context.Context, tx pgx.Tx, submissionID, taskID, organizationID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_submission_items item
		SET snapshot_data = item.snapshot_data || jsonb_build_object(
		    '_inventory_before',
		    jsonb_build_object(
		        'system_code', source.system_code,
		        'system_name', source.system_name,
		        'short_name', COALESCE(source.short_name, ''),
		        'owner_name', COALESCE(source.owner_name, ''),
		        'owner_contact', COALESCE(source.owner_contact, ''),
		        'system_status', source.system_status,
		        'system_level', source.system_level,
		        'deployment_type', COALESCE(source.deployment_type, ''),
		        'internet_exposure', source.internet_exposure,
		        'description', COALESCE(source.description, ''),
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
		        JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		        WHERE value.resource_type = 'business_system' AND value.resource_id = source.id
		    ), '{}'::jsonb) || jsonb_build_object(
		        '_custom_field_labels', COALESCE((
		            SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		            FROM custom_field_values value
		            JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		            WHERE value.resource_type = 'business_system' AND value.resource_id = source.id
		        ), '{}'::jsonb)
		    )
		)
		FROM inventory_business_system_drafts draft
		JOIN business_systems source ON source.id = draft.source_system_id
		LEFT JOIN business_system_data_security_profiles profile ON profile.business_system_id = source.id
		WHERE item.submission_id = $1::uuid
		  AND item.resource_type = 'business_system'
		  AND item.resource_id = draft.id
		  AND draft.task_id = $2::uuid
		  AND draft.organization_id = $3::uuid
		  AND draft.operation IN ('update', 'delete')
	`, submissionID, taskID, organizationID); err != nil {
		return fmt.Errorf("attach business system draft before snapshot: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE inventory_submission_items item
		SET snapshot_data = item.snapshot_data || jsonb_build_object(
		    '_inventory_before',
		    jsonb_build_object(
		        'asset_type', source.asset_type,
		        'asset_value', source.asset_value,
		        'asset_name', COALESCE(source.asset_name, ''),
		        'owner_name', COALESCE(owner.owner_name, ''),
		        'environment', source.environment,
		        'network_scope', source.network_scope,
		        'network_area_name', COALESCE(area.area_name, ''),
		        'status', source.status,
		        'importance', source.importance,
		        'expires_at', source.expires_at,
		        'linked_system_codes', COALESCE((
		            SELECT jsonb_agg(system.system_code ORDER BY system.system_code)
		            FROM system_assets link
		            JOIN business_systems system ON system.id = link.business_system_id
		            WHERE link.asset_id = source.id AND system.deleted_at IS NULL
		        ), '[]'::jsonb),
		        'notes', COALESCE(source.notes, '')
		    ) || COALESCE((
		        SELECT jsonb_object_agg('custom:' || definition.field_key, value.value)
		        FROM custom_field_values value
		        JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		        WHERE value.resource_type = 'asset' AND value.resource_id = source.id
		    ), '{}'::jsonb) || jsonb_build_object(
		        '_custom_field_labels', COALESCE((
		            SELECT jsonb_object_agg('custom:' || definition.field_key, definition.field_name)
		            FROM custom_field_values value
		            JOIN custom_field_definitions definition ON definition.id = value.field_definition_id
		            WHERE value.resource_type = 'asset' AND value.resource_id = source.id
		        ), '{}'::jsonb)
		    )
		)
		FROM inventory_asset_drafts draft
		JOIN assets source ON source.id = draft.source_asset_id
		LEFT JOIN asset_organizations owner ON owner.asset_id = source.id AND owner.is_primary
		LEFT JOIN asset_network_areas area ON area.id = source.network_area_id
		WHERE item.submission_id = $1::uuid
		  AND item.resource_type = 'network_asset'
		  AND item.resource_id = draft.id
		  AND draft.task_id = $2::uuid
		  AND draft.organization_id = $3::uuid
		  AND draft.operation IN ('update', 'delete')
	`, submissionID, taskID, organizationID); err != nil {
		return fmt.Errorf("attach network asset draft before snapshot: %w", err)
	}
	return nil
}

func writeSubmissionAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, taskID, note, status string, snapshot SubmissionMetadata, requestID, ipAddress string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'inventory_task.submit', 'inventory_task', $3, '提交填报任务',
		        jsonb_build_object(
				'note', $4::text, 'status', $5::text,
				'submission_id', $6::text, 'submission_version', $7::int,
				'previous_submission_id', $8::text,
				'system_count', $9::bigint, 'asset_count', $10::bigint
			), NULLIF($11, '')::inet, NULLIF($12, ''))
	`, principal.UserID, organizationID, taskID, note, status, snapshot.ID, snapshot.Version,
		snapshot.PreviousSubmissionID, snapshot.SystemCount, snapshot.AssetCount, ipAddress, requestID); err != nil {
		return fmt.Errorf("write inventory submission audit: %w", err)
	}
	return nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func updateSubmissionReviewStatus(ctx context.Context, tx pgx.Tx, submissionID, status, reviewerID, returnReason string) error {
	if status != "approved" && status != "returned" {
		return ErrInvalid
	}
	result, err := tx.Exec(ctx, `
		UPDATE inventory_submissions
		SET review_status=$2,
		    reviewed_at=now(),
		    reviewed_by=NULLIF($3, '')::uuid,
		    return_reason=CASE WHEN $2='returned' THEN NULLIF($4, '') ELSE NULL END
		WHERE id::text=$1
	`, submissionID, status, reviewerID, strings.TrimSpace(returnReason))
	if err != nil {
		return fmt.Errorf("update inventory submission review status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
