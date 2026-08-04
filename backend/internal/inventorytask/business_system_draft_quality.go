package inventorytask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"netassets/internal/customfield"
	"netassets/internal/dataquality"
)

type businessSystemDraftQualityRecord struct {
	fields map[string]any
	custom map[string]any
}

func configuredBusinessSystemDraftMissingCount(ctx context.Context, q qualityCheckQuerier, rule dataquality.Rule, taskID, organizationID string) (int64, error) {
	rows, err := q.Query(ctx, `
		SELECT system_code,system_name,COALESCE(owner_name,''),COALESCE(owner_contact,''),system_status,system_level,
		       COALESCE(deployment_type,''),internet_exposure,contains_personal_information,
		       contains_sensitive_personal_information,data_volume_over_100k,contains_important_data,
		       contains_core_data,custom_values
		FROM inventory_business_system_drafts
		WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status IN ('editing','returned')
	`, taskID, organizationID)
	if err != nil {
		return 0, fmt.Errorf("load business system drafts for configured quality rule %s: %w", rule.ID, err)
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var code, name, ownerName, ownerContact, status, systemLevel, deploymentType, internetExposure string
		var containsPersonalInformation, containsSensitivePersonalInformation, dataVolumeOver100K string
		var containsImportantData, containsCoreData string
		var rawCustom []byte
		if err := rows.Scan(
			&code, &name, &ownerName, &ownerContact, &status, &systemLevel, &deploymentType, &internetExposure,
			&containsPersonalInformation, &containsSensitivePersonalInformation, &dataVolumeOver100K,
			&containsImportantData, &containsCoreData, &rawCustom,
		); err != nil {
			return 0, fmt.Errorf("scan business system draft quality fields: %w", err)
		}
		var values []customfield.Value
		if err := json.Unmarshal(rawCustom, &values); err != nil {
			return 0, fmt.Errorf("decode business system draft quality custom fields: %w", err)
		}
		customValues := make(map[string]any, len(values))
		for _, value := range values {
			customValues[value.FieldKey] = value.Value
		}
		record := businessSystemDraftQualityRecord{
			fields: map[string]any{
				"code": code, "name": name, "owner_name": ownerName, "owner_contact": ownerContact,
				"status": status, "system_level": systemLevel, "deployment_type": deploymentType,
				"internet_exposure": internetExposure, "contains_personal_information": containsPersonalInformation,
				"contains_sensitive_personal_information": containsSensitivePersonalInformation,
				"data_volume_over_100k":                   dataVolumeOver100K, "contains_important_data": containsImportantData,
				"contains_core_data": containsCoreData, "organization_id": organizationID,
			},
			custom: customValues,
		}
		if businessSystemDraftMatchesQualityRule(record, rule) {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate business system draft quality fields: %w", err)
	}
	return count, nil
}

func businessSystemDraftMatchesQualityRule(record businessSystemDraftQualityRecord, rule dataquality.Rule) bool {
	if !businessSystemDraftTargetMissing(record, rule.TargetField) {
		return false
	}
	for _, condition := range rule.Conditions {
		if !businessSystemDraftConditionMatches(record, condition) {
			return false
		}
	}
	return true
}

func (record businessSystemDraftQualityRecord) value(field string) any {
	if strings.HasPrefix(field, "custom:") {
		return record.custom[strings.TrimPrefix(field, "custom:")]
	}
	return record.fields[field]
}

func businessSystemDraftTargetMissing(record businessSystemDraftQualityRecord, field string) bool {
	if strings.HasPrefix(field, "custom:") {
		return qualityValueMissing(record.value(field))
	}
	switch field {
	case "internet_exposure", "contains_personal_information", "contains_sensitive_personal_information",
		"data_volume_over_100k", "contains_important_data", "contains_core_data":
		return record.value(field) == "unknown"
	default:
		return qualityValueMissing(record.value(field))
	}
}

func businessSystemDraftConditionMatches(record businessSystemDraftQualityRecord, condition dataquality.RuleCondition) bool {
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
