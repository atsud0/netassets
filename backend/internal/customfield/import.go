package customfield

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
)

const ImportHeaderPrefix = "custom:"

// ImportHeader is the stable, machine-readable column name used by CSV/XLSX
// templates. Field display names can change, but field keys are immutable.
func ImportHeader(d Definition) string { return ImportHeaderPrefix + d.FieldKey }

// ActiveDefinitionsForOrganization returns enabled definitions after verifying
// that the caller may maintain the target organization. Definitions are
// group-wide today, but the organization check prevents template disclosure
// and import attempts outside the caller's data scope.
func (s *Service) ActiveDefinitionsForOrganization(ctx context.Context, p auth.Principal, typ, organizationID string) ([]Definition, error) {
	permission := "asset.manage"
	if typ == "business_system" {
		permission = "system.manage"
	}
	if typ != "asset" && typ != "business_system" || !p.HasPermission(permission) {
		return nil, ErrForbidden
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, p, organizationID, permission)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	return s.activeDefinitions(ctx, typ)
}

// EnabledDefinitions loads current enabled fields for trusted ledger/export services.
func (s *Service) EnabledDefinitions(ctx context.Context, typ string) ([]Definition, error) {
	if typ != "asset" && typ != "business_system" {
		return nil, ErrInvalid
	}
	return s.activeDefinitions(ctx, typ)
}

type definitionQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Service) activeDefinitions(ctx context.Context, typ string) ([]Definition, error) {
	return enabledDefinitions(ctx, s.pool, typ)
}

// EnabledDefinitionsInTx loads enabled definitions through the caller's transaction,
// keeping draft publication and custom-field validation on one consistent snapshot.
func EnabledDefinitionsInTx(ctx context.Context, tx pgx.Tx, typ string) ([]Definition, error) {
	if typ != "asset" && typ != "business_system" {
		return nil, ErrInvalid
	}
	return enabledDefinitions(ctx, tx, typ)
}

func enabledDefinitions(ctx context.Context, q definitionQuerier, typ string) ([]Definition, error) {
	rows, err := q.Query(ctx, `SELECT id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at FROM custom_field_definitions WHERE resource_type=$1 AND is_enabled ORDER BY sort_order,created_at`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		var raw []byte
		if err := rows.Scan(&d.ID, &d.ResourceType, &d.FieldKey, &d.FieldName, &d.Description, &d.FieldType, &raw, &d.IsRequired, &d.IsEnabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &d.Options); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ImportValues converts custom:* spreadsheet cells to the JSON values used by
// custom_field_values, including type and option validation.
func ImportValues(definitions []Definition, headers, row []string) ([]Value, error) {
	byKey := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byKey[definition.FieldKey] = definition
	}
	indexes := make(map[string]int)
	for index, header := range headers {
		key := strings.TrimSpace(strings.TrimPrefix(header, "\ufeff"))
		if !strings.HasPrefix(strings.ToLower(key), ImportHeaderPrefix) {
			continue
		}
		fieldKey := strings.TrimSpace(key[len(ImportHeaderPrefix):])
		if _, ok := byKey[fieldKey]; !ok {
			return nil, fmt.Errorf("未知或已停用的自定义字段：%s", fieldKey)
		}
		if _, duplicate := indexes[fieldKey]; duplicate {
			return nil, fmt.Errorf("自定义字段列重复：%s", fieldKey)
		}
		indexes[fieldKey] = index
	}

	values := make([]Value, 0, len(indexes))
	for _, definition := range definitions {
		index, present := indexes[definition.FieldKey]
		raw := ""
		if present && index < len(row) {
			raw = strings.TrimSpace(row[index])
		}
		if raw == "" {
			if definition.IsRequired {
				return nil, fmt.Errorf("自定义字段“%s”不能为空", definition.FieldName)
			}
			continue
		}
		value, err := parseImportValue(definition, raw)
		if err != nil {
			return nil, fmt.Errorf("自定义字段“%s”：%w", definition.FieldName, err)
		}
		values = append(values, Value{FieldKey: definition.FieldKey, Value: value})
	}
	return values, nil
}

func parseImportValue(d Definition, raw string) (any, error) {
	switch d.FieldType {
	case "text", "textarea":
		return raw, nil
	case "date":
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return nil, fmt.Errorf("日期须为 YYYY-MM-DD")
		}
		return raw, nil
	case "number":
		value, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", ""), 64)
		if err != nil {
			return nil, fmt.Errorf("须为数字")
		}
		return value, nil
	case "boolean":
		switch strings.ToLower(raw) {
		case "是", "true", "1", "yes":
			return true, nil
		case "否", "false", "0", "no":
			return false, nil
		default:
			return nil, fmt.Errorf("须填写 是/否")
		}
	case "select":
		if containsOption(d.Options, raw) {
			return raw, nil
		}
		return nil, fmt.Errorf("须为以下选项之一：%s", strings.Join(d.Options, "、"))
	case "multiselect":
		parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '；' || r == ';' || r == ',' || r == '，' })
		values := make([]any, 0, len(parts))
		seen := make(map[string]struct{}, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !containsOption(d.Options, part) {
				return nil, fmt.Errorf("包含无效选项“%s”（可选：%s）", part, strings.Join(d.Options, "、"))
			}
			if _, exists := seen[part]; !exists {
				seen[part] = struct{}{}
				values = append(values, part)
			}
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("至少选择一项")
		}
		return values, nil
	default:
		return nil, fmt.Errorf("字段类型不支持")
	}
}

func containsOption(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

// ExportValue renders a stored custom-field JSON value as a spreadsheet cell.
func ExportValue(d Definition, value any) string {
	if value == nil {
		return ""
	}
	switch d.FieldType {
	case "boolean":
		if value, ok := value.(bool); ok {
			if value {
				return "是"
			}
			return "否"
		}
	case "multiselect":
		if values, ok := value.([]any); ok {
			items := make([]string, 0, len(values))
			for _, item := range values {
				if text, ok := item.(string); ok {
					items = append(items, text)
				}
			}
			return strings.Join(items, "；")
		}
	case "number":
		switch number := value.(type) {
		case float64:
			return strconv.FormatFloat(number, 'f', -1, 64)
		case float32:
			return strconv.FormatFloat(float64(number), 'f', -1, 64)
		}
	}
	return fmt.Sprint(value)
}

// ValuesForResources loads enabled custom-field values in one query for use in
// ledger exports. The returned map is resource ID -> field key -> JSON value.
func (s *Service) ValuesForResources(ctx context.Context, typ string, resourceIDs []string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT v.resource_id::text, d.field_key, v.value
		FROM custom_field_values v
		JOIN custom_field_definitions d ON d.id = v.field_definition_id
		WHERE v.resource_type = $1 AND d.is_enabled AND v.resource_id = ANY($2::uuid[])
	`, typ, resourceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var resourceID, fieldKey string
		var raw []byte
		if err := rows.Scan(&resourceID, &fieldKey, &raw); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if result[resourceID] == nil {
			result[resourceID] = make(map[string]any)
		}
		result[resourceID][fieldKey] = value
	}
	return result, rows.Err()
}

// ValuesInTx loads enabled custom-field values through the caller's transaction.
// It is used when a formal resource is copied into a working/reporting version.
func ValuesInTx(ctx context.Context, tx pgx.Tx, typ, resourceID string) ([]Value, error) {
	if typ != "asset" && typ != "business_system" {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT d.field_key,v.value FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type=$1 AND v.resource_id=$2::uuid AND d.is_enabled ORDER BY d.sort_order,d.created_at`, typ, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Value, 0)
	for rows.Next() {
		var value Value
		var raw []byte
		if err := rows.Scan(&value.FieldKey, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &value.Value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// SaveImportedValuesInTx writes already-normalized values while the caller's
// resource-creation transaction is open. It intentionally does not emit one
// audit record per cell: the surrounding import audit and resource-create audit
// identify the operation. This keeps one spreadsheet row atomic.
func SaveImportedValuesInTx(ctx context.Context, tx pgx.Tx, typ, resourceID string, definitions []Definition, values []Value, updatedBy string) error {
	if typ != "asset" && typ != "business_system" {
		return ErrInvalid
	}
	byKey, provided, err := ValidateValues(definitions, values)
	if err != nil {
		return err
	}
	for key, value := range provided {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO custom_field_values(resource_type,resource_id,field_definition_id,value,updated_by)VALUES($1,$2::uuid,$3::uuid,$4::jsonb,$5::uuid) ON CONFLICT(resource_id,field_definition_id) DO UPDATE SET value=EXCLUDED.value,updated_by=EXCLUDED.updated_by,updated_at=now()`, typ, resourceID, byKey[key].ID, raw, updatedBy); err != nil {
			return err
		}
	}
	return nil
}

// ValidateValues validates API/import-style custom values without persisting
// them. It is used by inventory drafts so required and typed group fields are
// enforced before submission.
func ValidateValues(definitions []Definition, values []Value) (map[string]Definition, map[string]any, error) {
	byKey := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byKey[definition.FieldKey] = definition
	}
	provided := make(map[string]any, len(values))
	for _, value := range values {
		definition, ok := byKey[value.FieldKey]
		if !ok || !validFieldValue(definition, value.Value) {
			return nil, nil, ErrInvalid
		}
		if _, duplicate := provided[value.FieldKey]; duplicate {
			return nil, nil, ErrInvalid
		}
		provided[value.FieldKey] = value.Value
	}
	for _, definition := range definitions {
		if definition.IsRequired && isBlank(provided[definition.FieldKey]) {
			return nil, nil, ErrInvalid
		}
	}
	return byKey, provided, nil
}
