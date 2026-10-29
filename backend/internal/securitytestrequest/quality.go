package securitytestrequest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/dataquality"
)

// ScopeQualityIssue mirrors the inventory task quality issue shape so the
// frontend can reuse its rendering.
type ScopeQualityIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Count    int    `json:"count"`
}

type ScopeQualityCheck struct {
	AssetCount   int                 `json:"asset_count"`
	SystemCount  int                 `json:"system_count"`
	Issues       []ScopeQualityIssue `json:"issues"`
	ErrorCount   int                 `json:"error_count"`
	WarningCount int                 `json:"warning_count"`
	Passed       bool                `json:"passed"`
}

// ScopeQualityError is returned by Submit when required fields are missing.
// The HTTP layer surfaces the embedded check so the applicant can fix scope
// data before re-submitting.
type ScopeQualityError struct {
	Check ScopeQualityCheck
}

func (e *ScopeQualityError) Error() string {
	return fmt.Sprintf("security test request scope quality check failed: %d error(s)", e.Check.ErrorCount)
}

// QualityCheck previews the submit-time completeness rules for one request.
// The same evaluation runs inside Submit, so the preview is authoritative.
func (s *Service) QualityCheck(ctx context.Context, principal auth.Principal, id string) (ScopeQualityCheck, error) {
	if _, isApplicant := applicantOrg(principal); !isApplicant && !canManage(principal) {
		return ScopeQualityCheck{}, ErrForbidden
	}
	if !validUUID(id) {
		return ScopeQualityCheck{}, invalidInput("申请标识格式不正确")
	}
	item, err := s.get(ctx, id)
	if err != nil {
		return ScopeQualityCheck{}, err
	}
	if err := ensureViewable(principal, item); err != nil {
		return ScopeQualityCheck{}, err
	}
	return runScopeQualityCheck(ctx, s.pool, item)
}

// runScopeQualityCheck applies the data-quality required-field semantics to
// the request scope: fixed owner/linkage/data-security checks plus every
// configured group rule, evaluated against live ledger data. Severity error
// blocks submission; warnings are recorded but allowed through.
func runScopeQualityCheck(ctx context.Context, q scopeQuerier, item Request) (ScopeQualityCheck, error) {
	result := ScopeQualityCheck{SystemCount: 1, Issues: make([]ScopeQualityIssue, 0)}
	publishedIDs, workingIDs, err := scopeIDs(ctx, q, item.ID)
	if err != nil {
		return ScopeQualityCheck{}, err
	}
	result.AssetCount = len(publishedIDs) + len(workingIDs)

	add := func(code, severity, message string, count int) {
		if count <= 0 {
			return
		}
		result.Issues = append(result.Issues, ScopeQualityIssue{Code: code, Severity: severity, Message: message, Count: count})
		if severity == "error" {
			result.ErrorCount++
		} else {
			result.WarningCount++
		}
	}

	var missingPublishedOwner, unlinkedPublished int
	if len(publishedIDs) > 0 {
		if err := q.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE btrim(COALESCE(ao.owner_name, '')) = ''),
			       count(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM system_assets sa WHERE sa.asset_id=a.id))
			FROM security_test_request_scope_assets r
			JOIN assets a ON a.id=r.published_asset_id
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
			WHERE r.request_id=$1::uuid`, item.ID).Scan(&missingPublishedOwner, &unlinkedPublished); err != nil {
			return ScopeQualityCheck{}, fmt.Errorf("check published scope assets: %w", err)
		}
	}
	var missingWorkingOwner, unlinkedWorking int
	if len(workingIDs) > 0 {
		if err := q.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE btrim(COALESCE(d.owner_name, '')) = ''),
			       count(*) FILTER (WHERE d.business_system_id IS NULL AND d.business_system_draft_id IS NULL)
			FROM security_test_request_scope_assets r
			JOIN inventory_asset_drafts d ON d.id=r.working_asset_id
			WHERE r.request_id=$1::uuid`, item.ID).Scan(&missingWorkingOwner, &unlinkedWorking); err != nil {
			return ScopeQualityCheck{}, fmt.Errorf("check working scope assets: %w", err)
		}
	}
	add("missing_asset_owner", "error", "网络资产负责人未填写", missingPublishedOwner+missingWorkingOwner)
	add("unlinked_public_asset", "warning", "公网资产尚未关联业务系统", unlinkedPublished+unlinkedWorking)

	missingOwner, missingContact, unknownDataSecurity, err := businessSystemGaps(ctx, q, item)
	if err != nil {
		return ScopeQualityCheck{}, err
	}
	add("missing_system_owner", "error", "业务系统负责人或联系方式未填写完整", countIf(missingOwner)+countIf(missingContact))
	add("unknown_data_security", "error", "业务系统的数据安全属性仍有未确认项", countIf(unknownDataSecurity))

	rules, err := loadQualityRules(ctx, q)
	if err != nil {
		return ScopeQualityCheck{}, err
	}
	workingRecords, err := loadWorkingAssetRecords(ctx, q, workingIDs)
	if err != nil {
		return ScopeQualityCheck{}, err
	}
	for _, rule := range rules {
		var count int
		switch rule.ResourceType {
		case "asset":
			if len(publishedIDs) > 0 {
				published, err := publishedRuleMissingCount(ctx, q, rule, item.ApplicantOrganizationID, publishedIDs)
				if err != nil {
					return ScopeQualityCheck{}, err
				}
				count += published
			}
			for _, record := range workingRecords {
				if ruleRecordMatchesQualityRule(record, rule) {
					count++
				}
			}
		case "business_system":
			if item.BusinessSystemID != "" {
				published, err := businessSystemRuleMissingCount(ctx, q, rule, item.BusinessSystemID)
				if err != nil {
					return ScopeQualityCheck{}, err
				}
				count += published
			} else {
				record, err := loadBusinessSystemDraftRecord(ctx, q, item.BusinessSystemDraftID)
				if err != nil {
					return ScopeQualityCheck{}, err
				}
				if ruleRecordMatchesQualityRule(record, rule) {
					count++
				}
			}
		}
		add("configured_required_"+rule.ResourceType+"_"+strings.ReplaceAll(strings.TrimPrefix(rule.TargetField, "custom:"), ":", "_"),
			rule.Severity, "集团规则“"+rule.RuleName+"”要求填写，当前存在缺失项", count)
	}
	result.Passed = result.ErrorCount == 0
	return result, nil
}

func countIf(condition bool) int {
	if condition {
		return 1
	}
	return 0
}

func scopeIDs(ctx context.Context, q scopeQuerier, requestID string) (publishedIDs, workingIDs []string, err error) {
	rows, err := q.Query(ctx, `
		SELECT COALESCE(published_asset_id::text, ''), COALESCE(working_asset_id::text, '')
		FROM security_test_request_scope_assets WHERE request_id=$1::uuid`, requestID)
	if err != nil {
		return nil, nil, fmt.Errorf("load request scope for quality check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var published, working string
		if err := rows.Scan(&published, &working); err != nil {
			return nil, nil, fmt.Errorf("scan request scope id: %w", err)
		}
		if published != "" {
			publishedIDs = append(publishedIDs, published)
		} else {
			workingIDs = append(workingIDs, working)
		}
	}
	return publishedIDs, workingIDs, rows.Err()
}

// businessSystemGaps evaluates the fixed business system checks for the
// request's referenced system, published or working version.
func businessSystemGaps(ctx context.Context, q scopeQuerier, item Request) (missingOwner, missingContact, unknownDataSecurity bool, err error) {
	if item.BusinessSystemDraftID != "" {
		var ownerName, ownerContact string
		var containsPersonal, containsSensitive, dataVolume, importantData, coreData string
		err = q.QueryRow(ctx, `
			SELECT COALESCE(owner_name, ''), COALESCE(owner_contact, ''),
			       contains_personal_information, contains_sensitive_personal_information,
			       data_volume_over_100k, contains_important_data, contains_core_data
			FROM inventory_business_system_drafts WHERE id=$1::uuid`, item.BusinessSystemDraftID).Scan(
			&ownerName, &ownerContact, &containsPersonal, &containsSensitive, &dataVolume, &importantData, &coreData)
		if err != nil {
			return false, false, false, fmt.Errorf("load business system draft for quality check: %w", err)
		}
		return strings.TrimSpace(ownerName) == "", strings.TrimSpace(ownerContact) == "",
			containsPersonal == "unknown" || containsSensitive == "unknown" || dataVolume == "unknown" ||
				importantData == "unknown" || coreData == "unknown", nil
	}
	var ownerName, ownerContact string
	var containsPersonal, containsSensitive, dataVolume, importantData, coreData string
	err = q.QueryRow(ctx, `
		SELECT COALESCE(bs.owner_name, ''), COALESCE(bs.owner_contact, ''),
		       COALESCE(p.contains_personal_information, 'unknown'), COALESCE(p.contains_sensitive_personal_information, 'unknown'),
		       COALESCE(p.data_volume_over_100k, 'unknown'), COALESCE(p.contains_important_data, 'unknown'), COALESCE(p.contains_core_data, 'unknown')
		FROM business_systems bs
		LEFT JOIN business_system_data_security_profiles p ON p.business_system_id=bs.id
		WHERE bs.id=$1::uuid`, item.BusinessSystemID).Scan(
		&ownerName, &ownerContact, &containsPersonal, &containsSensitive, &dataVolume, &importantData, &coreData)
	if err != nil {
		return false, false, false, fmt.Errorf("load business system for quality check: %w", err)
	}
	return strings.TrimSpace(ownerName) == "", strings.TrimSpace(ownerContact) == "",
		containsPersonal == "unknown" || containsSensitive == "unknown" || dataVolume == "unknown" ||
			importantData == "unknown" || coreData == "unknown", nil
}

func loadQualityRules(ctx context.Context, q scopeQuerier) ([]dataquality.Rule, error) {
	rows, err := q.Query(ctx, `SELECT id::text, rule_name, resource_type, target_field, severity, conditions FROM data_quality_rules WHERE is_enabled AND rule_type='required' ORDER BY sort_order, created_at`)
	if err != nil {
		return nil, fmt.Errorf("load configured quality rules: %w", err)
	}
	defer rows.Close()
	rules := make([]dataquality.Rule, 0)
	for rows.Next() {
		var rule dataquality.Rule
		var rawConditions []byte
		if err := rows.Scan(&rule.ID, &rule.RuleName, &rule.ResourceType, &rule.TargetField, &rule.Severity, &rawConditions); err != nil {
			return nil, fmt.Errorf("scan configured quality rule: %w", err)
		}
		rule.RuleType = "required"
		if err := json.Unmarshal(rawConditions, &rule.Conditions); err != nil {
			return nil, fmt.Errorf("decode configured quality rule conditions: %w", err)
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// publishedRuleMissingCount reuses the active-scan rule query for published
// assets, restricted to the request scope.
func publishedRuleMissingCount(ctx context.Context, q scopeQuerier, rule dataquality.Rule, organizationID string, assetIDs []string) (int, error) {
	query, args, err := dataquality.BuildRuleIssueCountQuery(rule, organizationID)
	if err != nil {
		return 0, fmt.Errorf("build configured quality rule %s: %w", rule.ID, err)
	}
	args = append(args, assetIDs)
	query += fmt.Sprintf(" AND a.id = ANY($%d::uuid[])", len(args))
	var count int
	if err := q.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count configured quality rule %s: %w", rule.ID, err)
	}
	return count, nil
}

func businessSystemRuleMissingCount(ctx context.Context, q scopeQuerier, rule dataquality.Rule, businessSystemID string) (int, error) {
	query, args, err := dataquality.BuildRuleIssueCountQuery(rule, "")
	if err != nil {
		return 0, fmt.Errorf("build configured quality rule %s: %w", rule.ID, err)
	}
	args = append(args, businessSystemID)
	query += fmt.Sprintf(" AND bs.id = $%d::uuid", len(args))
	var count int
	if err := q.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count configured quality rule %s: %w", rule.ID, err)
	}
	return count, nil
}

// ruleRecord is the in-memory evaluation shape for working versions, mirroring
// the inventory task pre-submit checks so both flows judge the same rule the
// same way.
type ruleRecord struct {
	resourceType string
	fields       map[string]any
	custom       map[string]any
}

func (record ruleRecord) value(field string) any {
	if strings.HasPrefix(field, "custom:") {
		return record.custom[strings.TrimPrefix(field, "custom:")]
	}
	return record.fields[field]
}

func ruleRecordMatchesQualityRule(record ruleRecord, rule dataquality.Rule) bool {
	if !ruleRecordTargetMissing(record, rule.TargetField) {
		return false
	}
	for _, condition := range rule.Conditions {
		if !ruleRecordConditionMatches(record, condition) {
			return false
		}
	}
	return true
}

func ruleRecordTargetMissing(record ruleRecord, field string) bool {
	if strings.HasPrefix(field, "custom:") {
		return qualityValueMissing(record.value(field))
	}
	if field == "business_system_id" && record.resourceType == "asset" {
		return record.fields["has_business_system"] == false
	}
	switch field {
	case "internet_exposure", "contains_personal_information", "contains_sensitive_personal_information",
		"data_volume_over_100k", "contains_important_data", "contains_core_data", "network_scope":
		return record.value(field) == "unknown"
	default:
		return qualityValueMissing(record.value(field))
	}
}

func ruleRecordConditionMatches(record ruleRecord, condition dataquality.RuleCondition) bool {
	value := record.value(condition.Field)
	empty := qualityValueMissing(value)
	switch condition.Operator {
	case "empty":
		return len(condition.Values) == 0 && empty
	case "not_empty":
		return len(condition.Values) == 0 && !empty
	case "is_true":
		actual, ok := value.(bool)
		return len(condition.Values) == 0 && ok && actual
	case "is_false":
		actual, ok := value.(bool)
		return len(condition.Values) == 0 && ok && !actual
	case "eq", "ne":
		if len(condition.Values) != 1 || empty {
			return false
		}
		matched := qualityValueContains(value, []string{condition.Values[0]})
		if condition.Operator == "ne" {
			return !matched
		}
		return matched
	case "in", "not_in":
		if len(condition.Values) == 0 || empty {
			return false
		}
		matched := qualityValueContains(value, condition.Values)
		if condition.Operator == "not_in" {
			return !matched
		}
		return matched
	default:
		return false
	}
}

func loadWorkingAssetRecords(ctx context.Context, q scopeQuerier, workingIDs []string) ([]ruleRecord, error) {
	if len(workingIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT asset_type, asset_value, COALESCE(asset_name, ''), environment, network_scope,
		       COALESCE(network_area_id::text, ''), status, importance, COALESCE(owner_name, ''),
		       COALESCE(business_system_id::text, ''), custom_values
		FROM inventory_asset_drafts WHERE id = ANY($1::uuid[])`, workingIDs)
	if err != nil {
		return nil, fmt.Errorf("load working scope assets for quality check: %w", err)
	}
	defer rows.Close()
	records := make([]ruleRecord, 0, len(workingIDs))
	for rows.Next() {
		var assetType, assetValue, assetName, environment, networkScope, networkAreaID, status, importance, ownerName, businessSystemID string
		var rawCustom []byte
		if err := rows.Scan(&assetType, &assetValue, &assetName, &environment, &networkScope,
			&networkAreaID, &status, &importance, &ownerName, &businessSystemID, &rawCustom); err != nil {
			return nil, fmt.Errorf("scan working scope asset quality fields: %w", err)
		}
		custom, err := decodeCustomValues(rawCustom)
		if err != nil {
			return nil, err
		}
		records = append(records, ruleRecord{
			resourceType: "asset",
			fields: map[string]any{
				"asset_type": assetType, "value": assetValue, "name": assetName, "environment": environment,
				"network_scope": networkScope, "network_area_id": networkAreaID, "status": status,
				"importance": importance, "owner_name": ownerName, "business_system_id": businessSystemID,
				"has_business_system": businessSystemID != "",
			},
			custom: custom,
		})
	}
	return records, rows.Err()
}

func loadBusinessSystemDraftRecord(ctx context.Context, q scopeQuerier, draftID string) (ruleRecord, error) {
	var code, name, ownerName, ownerContact, systemStatus, systemLevel, deploymentType, internetExposure string
	var containsPersonal, containsSensitive, dataVolume, importantData, coreData string
	var rawCustom []byte
	err := q.QueryRow(ctx, `
		SELECT system_code, system_name, COALESCE(owner_name, ''), COALESCE(owner_contact, ''), system_status, system_level,
		       COALESCE(deployment_type, ''), internet_exposure, contains_personal_information,
		       contains_sensitive_personal_information, data_volume_over_100k, contains_important_data,
		       contains_core_data, custom_values
		FROM inventory_business_system_drafts WHERE id=$1::uuid`, draftID).Scan(
		&code, &name, &ownerName, &ownerContact, &systemStatus, &systemLevel, &deploymentType, &internetExposure,
		&containsPersonal, &containsSensitive, &dataVolume, &importantData, &coreData, &rawCustom)
	if err != nil {
		return ruleRecord{}, fmt.Errorf("load business system draft for quality check: %w", err)
	}
	custom, err := decodeCustomValues(rawCustom)
	if err != nil {
		return ruleRecord{}, err
	}
	return ruleRecord{
		resourceType: "business_system",
		fields: map[string]any{
			"code": code, "name": name, "owner_name": ownerName, "owner_contact": ownerContact,
			"status": systemStatus, "system_level": systemLevel, "deployment_type": deploymentType,
			"internet_exposure": internetExposure, "contains_personal_information": containsPersonal,
			"contains_sensitive_personal_information": containsSensitive, "data_volume_over_100k": dataVolume,
			"contains_important_data": importantData, "contains_core_data": coreData,
		},
		custom: custom,
	}, nil
}

func decodeCustomValues(raw []byte) (map[string]any, error) {
	var values []customfield.Value
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode draft custom fields: %w", err)
	}
	customValues := make(map[string]any, len(values))
	for _, value := range values {
		customValues[value.FieldKey] = value.Value
	}
	return customValues, nil
}

func qualityValueMissing(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	default:
		return false
	}
}

func qualityValueContains(value any, candidates []string) bool {
	matches := func(actual string) bool {
		for _, candidate := range candidates {
			if actual == candidate {
				return true
			}
		}
		return false
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if matches(fmt.Sprint(item)) {
				return true
			}
		}
		return false
	case []string:
		for _, item := range typed {
			if matches(item) {
				return true
			}
		}
		return false
	default:
		return matches(fmt.Sprint(value))
	}
}
