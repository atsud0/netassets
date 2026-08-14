package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/businesssystem"
	"netassets/internal/customfield"
)

type ResourceSubmissionRestoreResult struct {
	WorkingVersionID string   `json:"working_version_id"`
	PublishedID      string   `json:"published_id"`
	ResourceType     string   `json:"resource_type"`
	OrganizationID   string   `json:"organization_id"`
	ReportingStatus  string   `json:"reporting_status"`
	Warnings         []string `json:"warnings"`
}

type resourceSubmissionRestoreSnapshot struct {
	SubmissionID string
	TaskID       string
	TaskName     string
	Version      int
	Status       string
	Operation    string
	Data         map[string]any
}

// RestoreResourceSubmission turns one approved immutable submission snapshot
// into an organization-side unreported working version. It deliberately does
// not update the published group ledger; the restored version must be selected
// into a task, submitted and approved again.
func (s *Service) RestoreResourceSubmission(ctx context.Context, principal auth.Principal, resourceType, resourceID, submissionID, requestID, ipAddress string) (ResourceSubmissionRestoreResult, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	submissionID = strings.TrimSpace(submissionID)
	if !validSubmissionDiffResourceType(resourceType) || !inventoryResourceUUIDPattern.MatchString(resourceID) || !inventoryResourceUUIDPattern.MatchString(submissionID) {
		return ResourceSubmissionRestoreResult{}, ErrInvalid
	}
	if principal.HasRole("group_admin") || !principal.HasPermission("inventory.manage") {
		return ResourceSubmissionRestoreResult{}, ErrForbidden
	}
	permission := "asset.manage"
	if resourceType == "business_system" {
		permission = "system.manage"
	}
	if !principal.HasPermission(permission) {
		return ResourceSubmissionRestoreResult{}, ErrForbidden
	}

	target, err := s.loadResourceSubmissionTarget(ctx, resourceType, resourceID)
	if err != nil {
		return ResourceSubmissionRestoreResult{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, target.OrganizationID, permission)
	if err != nil {
		return ResourceSubmissionRestoreResult{}, err
	}
	if !allowed {
		return ResourceSubmissionRestoreResult{}, ErrForbidden
	}

	snapshot, err := s.loadResourceSubmissionRestoreSnapshot(ctx, resourceType, target, submissionID)
	if err != nil {
		return ResourceSubmissionRestoreResult{}, err
	}
	if snapshot.Status != "approved" || snapshot.Operation == "delete" {
		return ResourceSubmissionRestoreResult{}, ErrState
	}

	var workingVersionID, publishedID, reportingStatus string
	warnings := make([]string, 0)
	switch resourceType {
	case "network_asset":
		input, inputWarnings, buildErr := s.buildRestoredAssetInput(ctx, principal, target, snapshot.Data)
		if buildErr != nil {
			return ResourceSubmissionRestoreResult{}, buildErr
		}
		item, restoreErr := asset.NewService(s.pool).RestorePublishedVersion(ctx, principal, target.CanonicalID, input, requestID, ipAddress)
		if restoreErr != nil {
			return ResourceSubmissionRestoreResult{}, mapAssetRestoreError(restoreErr)
		}
		workingVersionID, publishedID, reportingStatus = item.ID, item.PublishedID, item.ReportingStatus
		warnings = append(warnings, inputWarnings...)
	case "business_system":
		input, inputWarnings, buildErr := s.buildRestoredBusinessSystemInput(ctx, principal, target, snapshot.Data)
		if buildErr != nil {
			return ResourceSubmissionRestoreResult{}, buildErr
		}
		item, restoreErr := businesssystem.NewService(s.pool).RestorePublishedVersion(ctx, principal, target.CanonicalID, input, requestID, ipAddress)
		if restoreErr != nil {
			return ResourceSubmissionRestoreResult{}, mapBusinessSystemRestoreError(restoreErr)
		}
		workingVersionID, publishedID, reportingStatus = item.ID, item.PublishedID, item.ReportingStatus
		warnings = append(warnings, inputWarnings...)
	}

	if _, err = s.pool.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'inventory_submission.resource_restore',$3,$4,
		       '从已发布上报历史恢复资源工作版本',
		       jsonb_build_object('submission_id',$5::text,'task_id',$6::text,'task_name',$7::text,
		                          'submission_version',$8::int,'published_id',$9::text,'working_version_id',$4::text),
		       NULLIF($10,'')::inet,NULLIF($11,''))
	`, principal.UserID, target.OrganizationID, resourceType, workingVersionID, snapshot.SubmissionID,
		snapshot.TaskID, snapshot.TaskName, snapshot.Version, publishedID, ipAddress, requestID); err != nil {
		// The resource-domain restore already writes an atomic restore audit. This
		// second record enriches it with the originating task/submission context;
		// failure here must not turn a completed restore into a retry-inducing 500.
		warnings = append(warnings, "资源已恢复为未上报工作版本，但来源提交审计补充记录失败，请联系管理员复核")
	}

	return ResourceSubmissionRestoreResult{
		WorkingVersionID: workingVersionID,
		PublishedID:      publishedID,
		ResourceType:     resourceType,
		OrganizationID:   target.OrganizationID,
		ReportingStatus:  reportingStatus,
		Warnings:         warnings,
	}, nil
}

func (s *Service) loadResourceSubmissionRestoreSnapshot(ctx context.Context, resourceType string, target resourceSubmissionTarget, submissionID string) (resourceSubmissionRestoreSnapshot, error) {
	var item resourceSubmissionRestoreSnapshot
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT submission.id::text, submission.task_id::text, task.task_name,
		       submission.submission_version, submission.review_status,
		       COALESCE(resource.snapshot_data->>'_inventory_operation',''), resource.snapshot_data
		FROM inventory_submission_items resource
		JOIN inventory_submissions submission ON submission.id=resource.submission_id
		JOIN inventory_tasks task ON task.id=submission.task_id
		WHERE submission.id::text=$1
		  AND resource.resource_type=$2
		  AND submission.organization_id=$3::uuid
		  AND (
		      resource.resource_id=$4::uuid
		      OR COALESCE(NULLIF(resource.snapshot_data->>'_source_resource_id',''),resource.resource_id::text)=$5
		  )
	`, submissionID, resourceType, target.OrganizationID, target.RequestedID, target.CanonicalID).Scan(
		&item.SubmissionID, &item.TaskID, &item.TaskName, &item.Version, &item.Status, &item.Operation, &raw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceSubmissionRestoreSnapshot{}, ErrNotFound
	}
	if err != nil {
		return resourceSubmissionRestoreSnapshot{}, fmt.Errorf("load resource submission restore snapshot: %w", err)
	}
	if err := json.Unmarshal(raw, &item.Data); err != nil {
		return resourceSubmissionRestoreSnapshot{}, fmt.Errorf("decode resource submission restore snapshot: %w", err)
	}
	item.Operation = strings.TrimSpace(item.Operation)
	return item, nil
}

func (s *Service) buildRestoredAssetInput(ctx context.Context, principal auth.Principal, target resourceSubmissionTarget, snapshot map[string]any) (asset.CreateInput, []string, error) {
	input := asset.CreateInput{
		Type:           restoreString(snapshot, "asset_type"),
		Value:          restoreString(snapshot, "asset_value"),
		Name:           restoreString(snapshot, "asset_name"),
		OrganizationID: target.OrganizationID,
		Environment:    restoreString(snapshot, "environment"),
		NetworkScope:   restoreString(snapshot, "network_scope"),
		Status:         restoreString(snapshot, "status"),
		Importance:     restoreString(snapshot, "importance"),
		OwnerName:      restoreString(snapshot, "owner_name"),
		Notes:          restoreString(snapshot, "notes"),
	}
	warnings := make([]string, 0)
	if rawExpiry := restoreString(snapshot, "expires_at"); rawExpiry != "" {
		expiresAt, err := time.Parse(time.RFC3339, rawExpiry)
		if err != nil {
			return asset.CreateInput{}, nil, ErrInvalid
		}
		input.ExpiresAt = &expiresAt
	}
	if areaName := restoreString(snapshot, "network_area_name"); areaName != "" {
		err := s.pool.QueryRow(ctx, `
			SELECT id::text FROM asset_network_areas
			WHERE organization_id=$1::uuid AND lower(area_name)=lower($2) AND status='enabled'
		`, target.OrganizationID, areaName).Scan(&input.NetworkAreaID)
		if errors.Is(err, pgx.ErrNoRows) {
			warnings = append(warnings, "历史网络区域“"+areaName+"”当前不存在或已停用，已留空，请人工复核")
		} else if err != nil {
			return asset.CreateInput{}, nil, fmt.Errorf("resolve historical network area: %w", err)
		}
	}
	codes := restoreStrings(snapshot["linked_system_codes"])
	if len(codes) > 0 {
		err := s.pool.QueryRow(ctx, `
			SELECT id::text FROM business_systems
			WHERE owner_organization_id=$1::uuid AND lower(system_code)=lower($2) AND deleted_at IS NULL
		`, target.OrganizationID, codes[0]).Scan(&input.BusinessSystemID)
		if errors.Is(err, pgx.ErrNoRows) {
			warnings = append(warnings, "历史关联业务系统“"+codes[0]+"”当前不存在，已留空，请人工复核")
		} else if err != nil {
			return asset.CreateInput{}, nil, fmt.Errorf("resolve historical business system: %w", err)
		}
		if len(codes) > 1 {
			warnings = append(warnings, "历史版本关联多个业务系统，工作版本仅恢复首个主关联，请人工复核")
		}
	}
	customValues, customWarnings, err := s.restoreCustomValues(ctx, principal, "asset", target.CanonicalID, snapshot)
	if err != nil {
		return asset.CreateInput{}, nil, err
	}
	input.CustomValues = customValues
	warnings = append(warnings, customWarnings...)
	if _, err := asset.PrepareCreateInput(&input); err != nil {
		return asset.CreateInput{}, nil, ErrInvalid
	}
	return input, warnings, nil
}

func (s *Service) buildRestoredBusinessSystemInput(ctx context.Context, principal auth.Principal, target resourceSubmissionTarget, snapshot map[string]any) (businesssystem.CreateInput, []string, error) {
	input := businesssystem.CreateInput{
		Code:                                 restoreString(snapshot, "system_code"),
		Name:                                 restoreString(snapshot, "system_name"),
		ShortName:                            restoreString(snapshot, "short_name"),
		OrganizationID:                       target.OrganizationID,
		OwnerName:                            restoreString(snapshot, "owner_name"),
		OwnerContact:                         restoreString(snapshot, "owner_contact"),
		Status:                               restoreString(snapshot, "system_status"),
		Level:                                restoreString(snapshot, "system_level"),
		DeploymentType:                       restoreString(snapshot, "deployment_type"),
		InternetExposure:                     restoreString(snapshot, "internet_exposure"),
		Description:                          restoreString(snapshot, "description"),
		ContainsPersonalInformation:          restoreString(snapshot, "contains_personal_information"),
		ContainsSensitivePersonalInformation: restoreString(snapshot, "contains_sensitive_personal_information"),
		DataVolumeOver100K:                   restoreString(snapshot, "data_volume_over_100k"),
		ContainsImportantData:                restoreString(snapshot, "contains_important_data"),
		ContainsCoreData:                     restoreString(snapshot, "contains_core_data"),
		DataStorageLocation:                  restoreString(snapshot, "data_storage_location"),
		SecurityNotes:                        restoreString(snapshot, "security_notes"),
	}
	if value, present := snapshot["personal_information_record_count"]; present && value != nil {
		count, ok := restoreInt64(value)
		if !ok {
			return businesssystem.CreateInput{}, nil, ErrInvalid
		}
		input.PersonalInformationRecordCount = &count
	}
	customValues, warnings, err := s.restoreCustomValues(ctx, principal, "business_system", target.CanonicalID, snapshot)
	if err != nil {
		return businesssystem.CreateInput{}, nil, err
	}
	input.CustomValues = customValues
	if err := businesssystem.PrepareCreateInput(&input); err != nil {
		return businesssystem.CreateInput{}, nil, ErrInvalid
	}
	return input, warnings, nil
}

func (s *Service) restoreCustomValues(ctx context.Context, principal auth.Principal, resourceType, resourceID string, snapshot map[string]any) ([]customfield.Value, []string, error) {
	service := customfield.NewService(s.pool)
	definitions, err := service.EnabledDefinitions(ctx, resourceType)
	if err != nil {
		return nil, nil, fmt.Errorf("load current custom field definitions: %w", err)
	}
	current, err := service.Values(ctx, principal, resourceType, resourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("load current custom field values: %w", err)
	}
	values, warnings, err := mergeRestoredCustomValues(definitions, current, snapshot)
	if err != nil {
		return nil, nil, err
	}
	return values, warnings, nil
}

func mergeRestoredCustomValues(definitions []customfield.Definition, current []customfield.Value, snapshot map[string]any) ([]customfield.Value, []string, error) {
	definitionsByKey := make(map[string]customfield.Definition, len(definitions))
	for _, definition := range definitions {
		definitionsByKey[definition.FieldKey] = definition
	}
	merged := make(map[string]any, len(current))
	for _, value := range current {
		if _, enabled := definitionsByKey[value.FieldKey]; enabled {
			merged[value.FieldKey] = value.Value
		}
	}
	warnings := make([]string, 0)
	for key, value := range snapshot {
		if !strings.HasPrefix(key, customFieldPrefix) {
			continue
		}
		fieldKey := strings.TrimPrefix(key, customFieldPrefix)
		definition, enabled := definitionsByKey[fieldKey]
		if !enabled {
			warnings = append(warnings, "历史自定义字段“"+restoreCustomFieldLabel(snapshot, key)+"”已停用，未自动恢复")
			continue
		}
		if _, _, err := customfield.ValidateValues([]customfield.Definition{definition}, []customfield.Value{{FieldKey: fieldKey, Value: value}}); err != nil {
			warnings = append(warnings, "历史自定义字段“"+definition.FieldName+"”的值已不符合当前配置，保留现值，请人工复核")
			continue
		}
		merged[fieldKey] = value
	}
	values := make([]customfield.Value, 0, len(merged))
	for _, definition := range definitions {
		if value, exists := merged[definition.FieldKey]; exists {
			values = append(values, customfield.Value{FieldKey: definition.FieldKey, Value: value})
		}
	}
	if _, _, err := customfield.ValidateValues(definitions, values); err != nil {
		return nil, nil, ErrConflict
	}
	return values, warnings, nil
}

func restoreCustomFieldLabel(snapshot map[string]any, key string) string {
	if labels, ok := snapshot[customFieldLabelsKey].(map[string]any); ok {
		if label, ok := labels[key].(string); ok && strings.TrimSpace(label) != "" {
			return strings.TrimSpace(label)
		}
	}
	return strings.TrimPrefix(key, customFieldPrefix)
}

func restoreString(snapshot map[string]any, key string) string {
	value, _ := snapshot[key].(string)
	return strings.TrimSpace(value)
}

func restoreStrings(value any) []string {
	items := make([]string, 0)
	switch values := value.(type) {
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				items = append(items, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				items = append(items, strings.TrimSpace(value))
			}
		}
	}
	return items
}

func restoreInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < math.MinInt64 || number > math.MaxInt64 {
			return 0, false
		}
		return int64(number), true
	case json.Number:
		result, err := number.Int64()
		return result, err == nil
	case int64:
		return number, true
	case int:
		return int64(number), true
	default:
		return 0, false
	}
}

func mapAssetRestoreError(err error) error {
	switch {
	case errors.Is(err, asset.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, asset.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, asset.ErrTaskBound), errors.Is(err, asset.ErrConflict):
		return ErrConflict
	case errors.Is(err, asset.ErrInvalid):
		return ErrInvalid
	default:
		return err
	}
}

func mapBusinessSystemRestoreError(err error) error {
	switch {
	case errors.Is(err, businesssystem.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, businesssystem.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, businesssystem.ErrConflict), errors.Is(err, businesssystem.ErrInUse):
		return ErrConflict
	case errors.Is(err, businesssystem.ErrInvalid):
		return ErrInvalid
	default:
		return err
	}
}
