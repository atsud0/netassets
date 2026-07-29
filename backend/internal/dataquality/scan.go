package dataquality

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
)

const MaxScanIssues = 500

var scanUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type ScanFilter struct {
	OrganizationID string
	ResourceType   string
	Severity       string
}

type ScanIssue struct {
	IssueID           string `json:"issue_id,omitempty"`
	Status            string `json:"status,omitempty"`
	RuleID            string `json:"rule_id"`
	RuleName          string `json:"rule_name"`
	RuleType          string `json:"rule_type"`
	Severity          string `json:"severity"`
	StaleAfterDays    int    `json:"stale_after_days,omitempty"`
	ExpiryWarningDays int    `json:"expiry_warning_days,omitempty"`
	ResourceType      string `json:"resource_type"`
	ResourceID        string `json:"resource_id"`
	ResourceName      string `json:"resource_name"`
	OrganizationID    string `json:"organization_id"`
	OrganizationCode  string `json:"organization_code"`
	OrganizationName  string `json:"organization_name"`
	TargetField       string `json:"target_field"`
}

type ScanSummary struct {
	AssetCount       int64 `json:"asset_count"`
	SystemCount      int64 `json:"system_count"`
	EnabledRuleCount int   `json:"enabled_rule_count"`
	TotalIssueCount  int64 `json:"total_issue_count"`
	ErrorCount       int64 `json:"error_count"`
	WarningCount     int64 `json:"warning_count"`
	AutoClosedCount  int64 `json:"auto_closed_count"`
	ReturnedCount    int   `json:"returned_count"`
	Truncated        bool  `json:"truncated"`
}

type ScanResult struct {
	BatchID string      `json:"batch_id"`
	Summary ScanSummary `json:"summary"`
	Issues  []ScanIssue `json:"issues"`
}

func (s *Service) Scan(ctx context.Context, p auth.Principal, filter ScanFilter, requestID, ipAddress string) (ScanResult, error) {
	if err := isAdmin(p); err != nil {
		return ScanResult{}, err
	}
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	filter.Severity = strings.TrimSpace(filter.Severity)
	if (filter.OrganizationID != "" && !scanUUIDPattern.MatchString(filter.OrganizationID)) ||
		(filter.ResourceType != "" && filter.ResourceType != "asset" && filter.ResourceType != "business_system") ||
		(filter.Severity != "" && filter.Severity != "error" && filter.Severity != "warning") {
		return ScanResult{}, ErrInvalid
	}
	if filter.OrganizationID != "" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1::uuid)`, filter.OrganizationID).Scan(&exists); err != nil {
			return ScanResult{}, fmt.Errorf("validate scan organization: %w", err)
		}
		if !exists {
			return ScanResult{}, ErrInvalid
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ScanResult{}, fmt.Errorf("begin data quality scan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := ScanResult{Issues: make([]ScanIssue, 0)}
	if filter.ResourceType == "" || filter.ResourceType == "asset" {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary WHERE a.deleted_at IS NULL AND (NULLIF($1::text,'') IS NULL OR ao.organization_id=$1::uuid)`, filter.OrganizationID).Scan(&result.Summary.AssetCount); err != nil {
			return ScanResult{}, fmt.Errorf("count scanned assets: %w", err)
		}
	}
	if filter.ResourceType == "" || filter.ResourceType == "business_system" {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM business_systems bs WHERE bs.deleted_at IS NULL AND (NULLIF($1::text,'') IS NULL OR bs.owner_organization_id=$1::uuid)`, filter.OrganizationID).Scan(&result.Summary.SystemCount); err != nil {
			return ScanResult{}, fmt.Errorf("count scanned business systems: %w", err)
		}
	}

	rules, err := scanRulesWith(ctx, tx, filter)
	if err != nil {
		return ScanResult{}, err
	}
	result.Summary.EnabledRuleCount = len(rules)
	if err := tx.QueryRow(ctx, `
		INSERT INTO data_quality_scan_batches(
			organization_id,resource_type,severity,asset_count,system_count,enabled_rule_count,created_by
		) VALUES(NULLIF($1,'')::uuid,NULLIF($2,''),NULLIF($3,''),$4,$5,$6,$7::uuid)
		RETURNING id::text
	`, filter.OrganizationID, filter.ResourceType, filter.Severity, result.Summary.AssetCount, result.Summary.SystemCount, result.Summary.EnabledRuleCount, p.UserID).Scan(&result.BatchID); err != nil {
		return ScanResult{}, fmt.Errorf("create data quality scan batch: %w", err)
	}

	for _, rule := range rules {
		issues, err := listRuleIssues(ctx, tx, rule, filter.OrganizationID)
		if err != nil {
			return ScanResult{}, err
		}
		for _, issue := range issues {
			var issueID string
			err := tx.QueryRow(ctx, `
				INSERT INTO data_quality_issues(
					rule_id,resource_type,resource_id,resource_name,organization_id,organization_code,organization_name,
					rule_name,target_field,severity,status,first_detected_batch_id,last_detected_batch_id,updated_by
				) VALUES($1::uuid,$2,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10,'pending',$11::uuid,$11::uuid,$12::uuid)
				ON CONFLICT(rule_id,resource_type,resource_id,organization_id) DO UPDATE SET
					resource_name=EXCLUDED.resource_name,organization_code=EXCLUDED.organization_code,
					organization_name=EXCLUDED.organization_name,rule_name=EXCLUDED.rule_name,
					target_field=EXCLUDED.target_field,severity=EXCLUDED.severity,
					last_detected_batch_id=EXCLUDED.last_detected_batch_id,last_detected_at=now(),
					occurrence_count=data_quality_issues.occurrence_count+1,
					status=CASE
						WHEN data_quality_issues.status='closed' THEN 'pending'
						WHEN data_quality_issues.status='exempted' AND data_quality_issues.exemption_expires_at IS NOT NULL AND data_quality_issues.exemption_expires_at<=now() THEN 'pending'
						ELSE data_quality_issues.status END,
					resolution_type=CASE
						WHEN data_quality_issues.status='closed' OR (data_quality_issues.status='exempted' AND data_quality_issues.exemption_expires_at IS NOT NULL AND data_quality_issues.exemption_expires_at<=now()) THEN NULL
						ELSE data_quality_issues.resolution_type END,
					closed_at=CASE WHEN data_quality_issues.status='closed' THEN NULL ELSE data_quality_issues.closed_at END,
					updated_by=EXCLUDED.updated_by,updated_at=now()
				RETURNING id::text
			`, rule.ID, issue.ResourceType, issue.ResourceID, issue.ResourceName, issue.OrganizationID, issue.OrganizationCode, issue.OrganizationName, rule.RuleName, rule.TargetField, rule.Severity, result.BatchID, p.UserID).Scan(&issueID)
			if err != nil {
				return ScanResult{}, fmt.Errorf("persist data quality issue: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO data_quality_scan_batch_issues(batch_id,issue_id) VALUES($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, result.BatchID, issueID); err != nil {
				return ScanResult{}, fmt.Errorf("link data quality scan issue: %w", err)
			}
			result.Summary.TotalIssueCount++
			if rule.Severity == "error" {
				result.Summary.ErrorCount++
			} else {
				result.Summary.WarningCount++
			}
		}
	}

	if err := tx.QueryRow(ctx, `
		WITH resolved AS (
			UPDATE data_quality_issues i SET
				status='closed',resolution_type='auto_resolved',closed_at=now(),reviewed_at=now(),reviewed_by=$5::uuid,
				review_note='重新巡检后未再命中该规则，系统自动关闭',updated_by=$5::uuid,updated_at=now()
			FROM data_quality_rules r
			WHERE i.rule_id=r.id AND r.is_enabled AND r.rule_type IN ('required','stale','expiry')
			  AND i.last_detected_batch_id<>$1::uuid
			  AND i.status IN ('pending','in_progress','pending_review')
			  AND (NULLIF($2::text,'') IS NULL OR i.organization_id=$2::uuid)
			  AND (NULLIF($3::text,'') IS NULL OR i.resource_type=$3)
			  AND (NULLIF($4::text,'') IS NULL OR i.severity=$4)
			RETURNING i.id
		), events AS (
			INSERT INTO data_quality_issue_events(issue_id,actor_user_id,event_type,to_status,note)
			SELECT id,$5::uuid,'auto_resolved','closed','重新巡检后未再命中该规则，系统自动关闭' FROM resolved
			RETURNING 1
		)
		SELECT count(*) FROM events
	`, result.BatchID, filter.OrganizationID, filter.ResourceType, filter.Severity, p.UserID).Scan(&result.Summary.AutoClosedCount); err != nil {
		return ScanResult{}, fmt.Errorf("auto close data quality issues: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE data_quality_scan_batches SET total_issue_count=$2,error_count=$3,warning_count=$4,auto_closed_count=$5 WHERE id=$1::uuid`, result.BatchID, result.Summary.TotalIssueCount, result.Summary.ErrorCount, result.Summary.WarningCount, result.Summary.AutoClosedCount); err != nil {
		return ScanResult{}, fmt.Errorf("complete data quality scan batch: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT i.id::text,i.status,i.rule_id::text,i.rule_name,r.rule_type,i.severity,COALESCE(r.stale_after_days,0),COALESCE(r.expiry_warning_days,0),i.resource_type,i.resource_id::text,
		       i.resource_name,i.organization_id::text,i.organization_code,i.organization_name,i.target_field
		FROM data_quality_scan_batch_issues bi
		JOIN data_quality_issues i ON i.id=bi.issue_id
		JOIN data_quality_rules r ON r.id=i.rule_id
		WHERE bi.batch_id=$1::uuid
		ORDER BY CASE i.severity WHEN 'error' THEN 0 ELSE 1 END,i.organization_code,i.resource_name
		LIMIT $2
	`, result.BatchID, MaxScanIssues)
	if err != nil {
		return ScanResult{}, fmt.Errorf("load data quality scan issues: %w", err)
	}
	for rows.Next() {
		var item ScanIssue
		if err := rows.Scan(&item.IssueID, &item.Status, &item.RuleID, &item.RuleName, &item.RuleType, &item.Severity, &item.StaleAfterDays, &item.ExpiryWarningDays, &item.ResourceType, &item.ResourceID, &item.ResourceName, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.TargetField); err != nil {
			rows.Close()
			return ScanResult{}, err
		}
		result.Issues = append(result.Issues, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ScanResult{}, err
	}
	rows.Close()
	result.Summary.ReturnedCount = len(result.Issues)
	result.Summary.Truncated = int64(len(result.Issues)) < result.Summary.TotalIssueCount

	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,NULLIF($2,'')::uuid,'data_quality.scan','data_quality_scan_batch',$3,'执行当前台账数据质量检测',jsonb_build_object('resource_type',$4::text,'severity',$5::text,'rule_count',$6::int,'issue_count',$7::bigint,'auto_closed_count',$8::bigint),NULLIF($9,'')::inet,NULLIF($10,''))`, p.UserID, filter.OrganizationID, result.BatchID, filter.ResourceType, filter.Severity, result.Summary.EnabledRuleCount, result.Summary.TotalIssueCount, result.Summary.AutoClosedCount, ipAddress, requestID); err != nil {
		return ScanResult{}, fmt.Errorf("write data quality scan audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ScanResult{}, fmt.Errorf("commit data quality scan: %w", err)
	}
	return result, nil
}

func (s *Service) scanRules(ctx context.Context, filter ScanFilter) ([]Rule, error) {
	return scanRulesWith(ctx, s.pool, filter)
}

func scanRulesWith(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, filter ScanFilter) ([]Rule, error) {
	rows, err := q.Query(ctx, `SELECT `+fields+` FROM data_quality_rules WHERE is_enabled AND rule_type IN ('required','stale','expiry') AND (NULLIF($1::text,'') IS NULL OR resource_type=$1) AND (NULLIF($2::text,'') IS NULL OR severity=$2) ORDER BY resource_type,sort_order,created_at`, filter.ResourceType, filter.Severity)
	if err != nil {
		return nil, fmt.Errorf("load scan rules: %w", err)
	}
	defer rows.Close()
	items := []Rule{}
	for rows.Next() {
		item, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func listRuleIssues(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, rule Rule, organizationID string) ([]ScanIssue, error) {
	from, condition, args, err := scanQueryParts(rule, organizationID)
	if err != nil {
		return nil, err
	}
	selectColumns := `a.id::text,COALESCE(NULLIF(a.asset_name,''),a.asset_value),o.id::text,o.organization_code,o.organization_name`
	if rule.ResourceType == "business_system" {
		selectColumns = `bs.id::text,bs.system_name,o.id::text,o.organization_code,o.organization_name`
	}
	rows, err := q.Query(ctx, `SELECT `+selectColumns+` `+from+` AND `+condition+` ORDER BY o.organization_code,2`, args...)
	if err != nil {
		return nil, fmt.Errorf("list rule %s issues: %w", rule.ID, err)
	}
	defer rows.Close()
	items := []ScanIssue{}
	for rows.Next() {
		item := ScanIssue{RuleID: rule.ID, RuleName: rule.RuleName, RuleType: rule.RuleType, Severity: rule.Severity, StaleAfterDays: rule.StaleAfterDays, ExpiryWarningDays: rule.ExpiryWarningDays, ResourceType: rule.ResourceType, TargetField: rule.TargetField}
		if err := rows.Scan(&item.ResourceID, &item.ResourceName, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// scanRuleIssues remains a bounded helper used by focused tests and callers that
// only need an immediate preview instead of persisted scan results.
func (s *Service) scanRuleIssues(ctx context.Context, rule Rule, organizationID string, limit int) (int64, []ScanIssue, error) {
	items, err := listRuleIssues(ctx, s.pool, rule, organizationID)
	if err != nil {
		return 0, nil, err
	}
	count := int64(len(items))
	if limit <= 0 {
		return count, nil, nil
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return count, items, nil
}

func scanQueryParts(rule Rule, organizationID string) (string, string, []any, error) {
	args := []any{organizationID}
	if rule.ResourceType == "asset" {
		from := `FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary JOIN organizations o ON o.id=ao.organization_id WHERE a.deleted_at IS NULL AND (NULLIF($1::text,'') IS NULL OR ao.organization_id=$1::uuid)`
		var issueCondition string
		if rule.RuleType == "stale" {
			if rule.TargetField != "updated_at" || rule.StaleAfterDays < 1 || rule.StaleAfterDays > 3650 {
				return "", "", nil, ErrInvalid
			}
			args = append(args, rule.StaleAfterDays)
			issueCondition = fmt.Sprintf(`a.updated_at < now() - ($%d::integer * interval '1 day')`, len(args))
		} else if rule.RuleType == "expiry" {
			if rule.TargetField != "expires_at" || rule.ExpiryWarningDays < 0 || rule.ExpiryWarningDays > 3650 {
				return "", "", nil, ErrInvalid
			}
			args = append(args, rule.ExpiryWarningDays)
			issueCondition = fmt.Sprintf(`a.status<>'archived' AND a.expires_at IS NOT NULL AND a.expires_at <= now() + ($%d::integer * interval '1 day')`, len(args))
		} else {
			if rule.RuleType != "required" {
				return "", "", nil, ErrInvalid
			}
			if strings.HasPrefix(rule.TargetField, "custom:") {
				args = append(args, strings.TrimPrefix(rule.TargetField, "custom:"))
				issueCondition = fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type='asset' AND v.resource_id=a.id AND d.field_key=$%d AND jsonb_typeof(v.value)<>'null' AND btrim(trim(both '"' from v.value::text))<>'' AND v.value<>'[]'::jsonb)`, len(args))
			} else {
				condition, ok := map[string]string{"value": "btrim(COALESCE(a.asset_value,''))=''", "name": "btrim(COALESCE(a.asset_name,''))=''", "network_scope": "a.network_scope='unknown'", "network_area_id": "a.network_area_id IS NULL", "owner_name": "btrim(COALESCE(ao.owner_name,''))=''", "business_system_id": "NOT EXISTS (SELECT 1 FROM system_assets sa WHERE sa.asset_id=a.id)", "importance": "a.importance IS NULL"}[rule.TargetField]
				if !ok {
					return "", "", nil, fmt.Errorf("unsupported asset quality target: %s", rule.TargetField)
				}
				issueCondition = condition
			}
		}
		condition, args, err := appendRuleConditions(rule.ResourceType, rule.Conditions, issueCondition, args)
		return from, condition, args, err
	}
	if rule.ResourceType == "business_system" {
		from := `FROM business_systems bs JOIN organizations o ON o.id=bs.owner_organization_id LEFT JOIN business_system_data_security_profiles p ON p.business_system_id=bs.id WHERE bs.deleted_at IS NULL AND (NULLIF($1::text,'') IS NULL OR bs.owner_organization_id=$1::uuid)`
		var issueCondition string
		if rule.RuleType == "stale" {
			if rule.TargetField != "updated_at" || rule.StaleAfterDays < 1 || rule.StaleAfterDays > 3650 {
				return "", "", nil, ErrInvalid
			}
			args = append(args, rule.StaleAfterDays)
			issueCondition = fmt.Sprintf(`bs.updated_at < now() - ($%d::integer * interval '1 day')`, len(args))
		} else {
			if rule.RuleType != "required" {
				return "", "", nil, ErrInvalid
			}
			if strings.HasPrefix(rule.TargetField, "custom:") {
				args = append(args, strings.TrimPrefix(rule.TargetField, "custom:"))
				issueCondition = fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type='business_system' AND v.resource_id=bs.id AND d.field_key=$%d AND jsonb_typeof(v.value)<>'null' AND btrim(trim(both '"' from v.value::text))<>'' AND v.value<>'[]'::jsonb)`, len(args))
			} else {
				condition, ok := map[string]string{"code": "btrim(COALESCE(bs.system_code,''))=''", "name": "btrim(COALESCE(bs.system_name,''))=''", "owner_name": "btrim(COALESCE(bs.owner_name,''))=''", "owner_contact": "btrim(COALESCE(bs.owner_contact,''))=''", "system_level": "bs.system_level IS NULL", "internet_exposure": "bs.internet_exposure='unknown'", "contains_personal_information": "COALESCE(p.contains_personal_information,'unknown')='unknown'", "contains_sensitive_personal_information": "COALESCE(p.contains_sensitive_personal_information,'unknown')='unknown'", "data_volume_over_100k": "COALESCE(p.data_volume_over_100k,'unknown')='unknown'", "contains_important_data": "COALESCE(p.contains_important_data,'unknown')='unknown'", "contains_core_data": "COALESCE(p.contains_core_data,'unknown')='unknown'"}[rule.TargetField]
				if !ok {
					return "", "", nil, fmt.Errorf("unsupported business system quality target: %s", rule.TargetField)
				}
				issueCondition = condition
			}
		}
		condition, args, err := appendRuleConditions(rule.ResourceType, rule.Conditions, issueCondition, args)
		return from, condition, args, err
	}
	return "", "", nil, ErrInvalid
}

// BuildRuleIssueCountQuery builds the same parameterized rule query used by
// active scans so inventory submissions cannot bypass conditional rules.
func BuildRuleIssueCountQuery(rule Rule, organizationID string) (string, []any, error) {
	from, condition, args, err := scanQueryParts(rule, organizationID)
	if err != nil {
		return "", nil, err
	}
	return `SELECT count(*) ` + from + ` AND ` + condition, args, nil
}

func appendRuleConditions(resourceType string, conditions []RuleCondition, issueCondition string, args []any) (string, []any, error) {
	parts := []string{issueCondition}
	for _, condition := range conditions {
		part, nextArgs, err := ruleConditionSQL(resourceType, condition, args)
		if err != nil {
			return "", nil, err
		}
		args = nextArgs
		parts = append(parts, "("+part+")")
	}
	return strings.Join(parts, " AND "), args, nil
}

func ruleConditionSQL(resourceType string, condition RuleCondition, args []any) (string, []any, error) {
	if strings.HasPrefix(condition.Field, "custom:") {
		resourceAlias := "a.id"
		if resourceType == "business_system" {
			resourceAlias = "bs.id"
		} else if resourceType != "asset" {
			return "", nil, ErrInvalid
		}
		return customConditionSQL(resourceType, resourceAlias, strings.TrimPrefix(condition.Field, "custom:"), condition, args)
	}

	expressions := map[string]map[string]string{
		"asset": {
			"asset_type":      "a.asset_type",
			"status":          "a.status",
			"environment":     "a.environment",
			"importance":      "a.importance",
			"network_scope":   "a.network_scope",
			"organization_id": "ao.organization_id::text",
			"network_area_id": "a.network_area_id::text",
		},
		"business_system": {
			"status":                        "bs.system_status",
			"system_level":                  "bs.system_level",
			"deployment_type":               "bs.deployment_type",
			"internet_exposure":             "bs.internet_exposure",
			"contains_personal_information": "p.contains_personal_information",
			"contains_sensitive_personal_information": "p.contains_sensitive_personal_information",
			"data_volume_over_100k":                   "p.data_volume_over_100k",
			"contains_important_data":                 "p.contains_important_data",
			"contains_core_data":                      "p.contains_core_data",
			"organization_id":                         "bs.owner_organization_id::text",
		},
	}
	if resourceType == "asset" && condition.Field == "business_system_id" {
		return assetBusinessSystemConditionSQL(condition, args)
	}
	if resourceType == "asset" && condition.Field == "has_business_system" {
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		exists := `EXISTS (SELECT 1 FROM system_assets sa WHERE sa.asset_id=a.id)`
		switch condition.Operator {
		case "is_true":
			return exists, args, nil
		case "is_false":
			return "NOT " + exists, args, nil
		default:
			return "", nil, ErrInvalid
		}
	}
	expression, ok := expressions[resourceType][condition.Field]
	if !ok {
		return "", nil, ErrInvalid
	}
	return scalarConditionSQL(expression, condition, args)
}

func assetBusinessSystemConditionSQL(condition RuleCondition, args []any) (string, []any, error) {
	base := `SELECT 1 FROM system_assets sa WHERE sa.asset_id=a.id`
	switch condition.Operator {
	case "empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `NOT EXISTS (` + base + `)`, args, nil
	case "not_empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `EXISTS (` + base + `)`, args, nil
	case "eq", "ne":
		if len(condition.Values) != 1 {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values[0])
		match := fmt.Sprintf(`EXISTS (%s AND sa.business_system_id::text=$%d)`, base, len(args))
		if condition.Operator == "ne" {
			match = `EXISTS (` + base + `) AND NOT ` + match
		}
		return match, args, nil
	case "in", "not_in":
		if len(condition.Values) == 0 || len(condition.Values) > maxConditionValues {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values)
		match := fmt.Sprintf(`EXISTS (%s AND sa.business_system_id::text=ANY($%d::text[]))`, base, len(args))
		if condition.Operator == "not_in" {
			match = `EXISTS (` + base + `) AND NOT ` + match
		}
		return match, args, nil
	default:
		return "", nil, ErrInvalid
	}
}

func scalarConditionSQL(expression string, condition RuleCondition, args []any) (string, []any, error) {
	switch condition.Operator {
	case "empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `NULLIF(btrim(COALESCE(` + expression + `,'')),'') IS NULL`, args, nil
	case "not_empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `NULLIF(btrim(COALESCE(` + expression + `,'')),'') IS NOT NULL`, args, nil
	case "eq", "ne":
		if len(condition.Values) != 1 {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values[0])
		operator := "="
		prefix := ""
		if condition.Operator == "ne" {
			operator = "<>"
			prefix = fmt.Sprintf(`NULLIF(btrim(COALESCE(%s,'')),'') IS NOT NULL AND `, expression)
		}
		return prefix + fmt.Sprintf(`COALESCE(%s,'') %s $%d`, expression, operator, len(args)), args, nil
	case "in", "not_in":
		if len(condition.Values) == 0 || len(condition.Values) > maxConditionValues {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values)
		operator := "= ANY"
		prefix := ""
		if condition.Operator == "not_in" {
			operator = "<> ALL"
			prefix = fmt.Sprintf(`NULLIF(btrim(COALESCE(%s,'')),'') IS NOT NULL AND `, expression)
		}
		return prefix + fmt.Sprintf(`COALESCE(%s,'') %s($%d::text[])`, expression, operator, len(args)), args, nil
	default:
		return "", nil, ErrInvalid
	}
}

func customConditionSQL(resourceType, resourceAlias, fieldKey string, condition RuleCondition, args []any) (string, []any, error) {
	if fieldKey == "" {
		return "", nil, ErrInvalid
	}
	args = append(args, fieldKey)
	keyPlaceholder := fmt.Sprintf("$%d", len(args))
	base := fmt.Sprintf(`SELECT 1 FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type='%s' AND v.resource_id=%s AND d.field_key=%s`, resourceType, resourceAlias, keyPlaceholder)
	nonBlank := `jsonb_typeof(v.value)<>'null' AND btrim(trim(both '"' from v.value::text))<>'' AND v.value<>'[]'::jsonb`

	switch condition.Operator {
	case "empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `NOT EXISTS (` + base + ` AND ` + nonBlank + `)`, args, nil
	case "not_empty":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		return `EXISTS (` + base + ` AND ` + nonBlank + `)`, args, nil
	case "is_true", "is_false":
		if len(condition.Values) != 0 {
			return "", nil, ErrInvalid
		}
		value := "true"
		if condition.Operator == "is_false" {
			value = "false"
		}
		return `EXISTS (` + base + ` AND v.value='` + value + `'::jsonb)`, args, nil
	case "eq", "ne":
		if len(condition.Values) != 1 {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values[0])
		operator := "="
		if condition.Operator == "ne" {
			operator = "<>"
		}
		return fmt.Sprintf(`EXISTS (%s AND %s AND trim(both '"' from v.value::text) %s $%d)`, base, nonBlank, operator, len(args)), args, nil
	case "in", "not_in":
		if len(condition.Values) == 0 || len(condition.Values) > maxConditionValues {
			return "", nil, ErrInvalid
		}
		args = append(args, condition.Values)
		match := fmt.Sprintf(`((jsonb_typeof(v.value)='array' AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(v.value) e(value) WHERE e.value=ANY($%d::text[]))) OR (jsonb_typeof(v.value)<>'array' AND trim(both '"' from v.value::text)=ANY($%d::text[])))`, len(args), len(args))
		if condition.Operator == "not_in" {
			match = "NOT " + match
		}
		return `EXISTS (` + base + ` AND ` + nonBlank + ` AND ` + match + `)`, args, nil
	default:
		return "", nil, ErrInvalid
	}
}
