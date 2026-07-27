package securityworkorder

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

const MaxFindingImportRows = 5000

type FindingImportError struct {
	Row     int    `json:"row"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type FindingImportResult struct {
	ID             string               `json:"id"`
	TestBatchID    string               `json:"test_batch_id"`
	TestBatchCode  string               `json:"test_batch_code,omitempty"`
	TestBatchName  string               `json:"test_batch_name,omitempty"`
	FileName       string               `json:"file_name"`
	FileType       string               `json:"file_type"`
	Status         string               `json:"status"`
	TotalCount     int                  `json:"total_count"`
	SuccessCount   int                  `json:"success_count"`
	FailureCount   int                  `json:"failure_count"`
	DuplicateCount int                  `json:"duplicate_count"`
	Errors         []FindingImportError `json:"errors"`
}

type FindingImportHistory struct {
	FindingImportResult
	CreatedByName string    `json:"created_by_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type findingImportRowError struct{ code, message string }

type findingImportScope struct {
	AssetID            string
	OrganizationID     string
	BusinessSystemID   string
	AssetTypeSnapshot  string
	AssetValueSnapshot string
	OrganizationName   string
	BusinessSystemName string
}

// FindingImportTemplate returns the standard security-finding import template.
func FindingImportTemplate(format string) ([]byte, string, string, error) {
	headers := []string{"资产类型", "资产值", "漏洞标题", "严重等级", "漏洞类型", "漏洞描述", "验证证据", "整改建议", "发现时间"}
	example := []string{"url", "https://example.com/login", "登录接口未授权访问", "高危", "越权访问", "未登录情况下可以读取用户信息", "GET /api/user/1 返回完整用户信息", "增加服务端身份认证和资源权限校验", "2026-07-22 10:00:00"}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xEF\xBB\xBF")
		writer := csv.NewWriter(&buffer)
		if err := writer.Write(headers); err != nil {
			return nil, "", "", err
		}
		if err := writer.Write(example); err != nil {
			return nil, "", "", err
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "text/csv; charset=utf-8", "漏洞导入模板.csv", nil
	case "xlsx", "":
		file := excelize.NewFile()
		defer file.Close()
		sheet := file.GetSheetName(0)
		for column, value := range headers {
			cell, _ := excelize.CoordinatesToCellName(column+1, 1)
			_ = file.SetCellValue(sheet, cell, value)
		}
		for column, value := range example {
			cell, _ := excelize.CoordinatesToCellName(column+1, 2)
			_ = file.SetCellValue(sheet, cell, value)
		}
		_ = file.SetColWidth(sheet, "A", "A", 12)
		_ = file.SetColWidth(sheet, "B", "C", 28)
		_ = file.SetColWidth(sheet, "D", "E", 14)
		_ = file.SetColWidth(sheet, "F", "H", 34)
		_ = file.SetColWidth(sheet, "I", "I", 22)
		var buffer bytes.Buffer
		if err := file.Write(&buffer); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "漏洞导入模板.xlsx", nil
	default:
		return nil, "", "", ErrInvalid
	}
}

func (s *Service) ImportFindings(ctx context.Context, principal auth.Principal, testBatchID, fileName string, reader io.Reader, requestID, ipAddress string) (FindingImportResult, error) {
	if !principal.HasRole("group_admin") {
		return FindingImportResult{}, ErrForbidden
	}
	testBatchID = strings.TrimSpace(testBatchID)
	if !validUUID(testBatchID) {
		return FindingImportResult{}, ErrInvalid
	}
	var batchCode, batchName, batchStatus string
	if err := s.pool.QueryRow(ctx, `SELECT batch_code, batch_name, status FROM security_test_batches WHERE id::text=$1`, testBatchID).Scan(&batchCode, &batchName, &batchStatus); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportResult{}, ErrNotFound
	} else if err != nil {
		return FindingImportResult{}, fmt.Errorf("load finding import batch: %w", err)
	}
	if batchStatus != "authorized" && batchStatus != "in_progress" && batchStatus != "completed" {
		return FindingImportResult{}, ErrState
	}

	rows, fileType, err := readFindingImportRows(fileName, reader)
	if err != nil {
		return FindingImportResult{}, ErrInvalid
	}
	rows = nonEmptyImportRows(rows)
	if len(rows) < 2 || len(rows)-1 > MaxFindingImportRows {
		return FindingImportResult{}, ErrInvalid
	}
	headers := mapFindingImportHeaders(rows[0])
	for _, required := range []string{"asset_type", "asset_value", "title", "severity", "description"} {
		if headers[required] < 0 {
			return FindingImportResult{}, ErrInvalid
		}
	}

	result := FindingImportResult{
		TestBatchID: testBatchID, TestBatchCode: batchCode, TestBatchName: batchName,
		FileName: filepath.Base(fileName), FileType: fileType, TotalCount: len(rows) - 1,
		Errors: make([]FindingImportError, 0),
	}
	for index, row := range rows[1:] {
		rowNumber := index + 2
		input, assetType, assetValue, rowErr := findingInputFromImportRow(row, headers, testBatchID)
		if rowErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: rowErr.code, Message: rowErr.message})
			continue
		}
		scope, err := s.findImportScope(ctx, testBatchID, assetType, assetValue)
		if errors.Is(err, pgx.ErrNoRows) {
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "asset_out_of_scope", Message: "资产不在所选测试批次的授权范围内，或当前资产已删除"})
			continue
		}
		if err != nil {
			return FindingImportResult{}, err
		}
		input.AssetID = scope.AssetID
		input.AssigneeOrganizationID = scope.OrganizationID
		input.BusinessSystemID = scope.BusinessSystemID

		var duplicate bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM security_findings
			WHERE test_batch_id::text=$1 AND asset_id::text=$2 AND lower(btrim(title))=lower(btrim($3)))`,
			testBatchID, scope.AssetID, input.Title).Scan(&duplicate); err != nil {
			return FindingImportResult{}, fmt.Errorf("check duplicate imported finding: %w", err)
		}
		if duplicate {
			result.DuplicateCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "duplicate", Message: "同一测试批次、资产和漏洞标题的安全发现已存在"})
			continue
		}

		_, err = s.CreateFinding(ctx, principal, input, requestID, ipAddress)
		switch {
		case err == nil:
			result.SuccessCount++
		case errors.Is(err, ErrNotFound):
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "reference_changed", Message: "资产、责任组织或业务系统信息已变化，请刷新测试范围后重试"})
		case errors.Is(err, ErrConflict):
			result.DuplicateCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "duplicate", Message: "相同安全发现已存在"})
		case errors.Is(err, ErrInvalid):
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "invalid", Message: "漏洞字段格式不正确"})
		default:
			return FindingImportResult{}, err
		}
	}
	result.Status = "completed"
	if result.SuccessCount == 0 {
		result.Status = "failed"
	} else if result.FailureCount+result.DuplicateCount > 0 {
		result.Status = "partial"
	}
	id, err := s.saveFindingImportResult(ctx, principal, result, requestID, ipAddress)
	if err != nil {
		return FindingImportResult{}, err
	}
	result.ID = id
	return result, nil
}

func (s *Service) findImportScope(ctx context.Context, batchID, assetType, assetValue string) (findingImportScope, error) {
	var item findingImportScope
	err := s.pool.QueryRow(ctx, `
		SELECT scope.asset_id::text, scope.organization_id::text, COALESCE(scope.business_system_id::text, ''),
		       scope.asset_type_snapshot, scope.asset_value_snapshot, scope.organization_name_snapshot,
		       COALESCE(scope.business_system_name_snapshot, '')
		FROM security_test_scope_assets scope
		JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
		JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled'
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
		WHERE scope.batch_id::text=$1 AND lower(scope.asset_type_snapshot)=lower($2)
		  AND lower(btrim(scope.asset_value_snapshot))=lower(btrim($3))
		ORDER BY scope.created_at LIMIT 1`, batchID, assetType, assetValue).Scan(
		&item.AssetID, &item.OrganizationID, &item.BusinessSystemID, &item.AssetTypeSnapshot,
		&item.AssetValueSnapshot, &item.OrganizationName, &item.BusinessSystemName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return findingImportScope{}, fmt.Errorf("match finding import scope: %w", err)
	}
	return item, err
}

func (s *Service) saveFindingImportResult(ctx context.Context, principal auth.Principal, result FindingImportResult, requestID, ipAddress string) (string, error) {
	errorsJSON, err := json.Marshal(result.Errors)
	if err != nil {
		return "", fmt.Errorf("encode finding import errors: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin finding import result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO security_finding_imports
		(test_batch_id, file_name, file_type, status, total_count, success_count, failure_count, duplicate_count, errors, created_by)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::uuid) RETURNING id::text`,
		result.TestBatchID, result.FileName, result.FileType, result.Status, result.TotalCount, result.SuccessCount,
		result.FailureCount, result.DuplicateCount, string(errorsJSON), principal.UserID).Scan(&id); err != nil {
		return "", fmt.Errorf("save finding import result: %w", err)
	}
	organizationID := principalOrganizationID(principal)
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, 'security_finding.import', 'security_finding_import', $3,
		'批量导入安全发现', jsonb_build_object('test_batch_id',$4::text,'file_name',$5::text,'total',$6::int,'success',$7::int,'failure',$8::int,'duplicate',$9::int),
		NULLIF($10,'')::inet, NULLIF($11,''))`, principal.UserID, organizationID, id, result.TestBatchID,
		result.FileName, result.TotalCount, result.SuccessCount, result.FailureCount, result.DuplicateCount, ipAddress, requestID); err != nil {
		return "", fmt.Errorf("write finding import audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit finding import result: %w", err)
	}
	return id, nil
}

func (s *Service) ListFindingImports(ctx context.Context, principal auth.Principal) ([]FindingImportHistory, error) {
	result, err := s.SearchFindingImports(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) SearchFindingImports(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[FindingImportHistory], error) {
	if !principal.HasRole("group_admin") {
		return pagination.Result[FindingImportHistory]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text, i.test_batch_id::text, b.batch_code, b.batch_name, i.file_name, i.file_type, i.status,
		       i.total_count, i.success_count, i.failure_count, i.duplicate_count, i.errors::text,
		       COALESCE(u.display_name,''), i.created_at, count(*) OVER ()
		FROM security_finding_imports i
		JOIN security_test_batches b ON b.id=i.test_batch_id
		LEFT JOIN users u ON u.id=i.created_by
		ORDER BY i.created_at DESC LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[FindingImportHistory]{}, fmt.Errorf("search finding imports: %w", err)
	}
	defer rows.Close()
	items := make([]FindingImportHistory, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item FindingImportHistory
		var errorsText string
		if err := rows.Scan(&item.ID, &item.TestBatchID, &item.TestBatchCode, &item.TestBatchName, &item.FileName,
			&item.FileType, &item.Status, &item.TotalCount, &item.SuccessCount, &item.FailureCount,
			&item.DuplicateCount, &errorsText, &item.CreatedByName, &item.CreatedAt, &total); err != nil {
			return pagination.Result[FindingImportHistory]{}, fmt.Errorf("scan finding import: %w", err)
		}
		item.Errors = make([]FindingImportError, 0)
		if err := json.Unmarshal([]byte(errorsText), &item.Errors); err != nil {
			return pagination.Result[FindingImportHistory]{}, fmt.Errorf("decode finding import errors: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[FindingImportHistory]{}, fmt.Errorf("iterate finding imports: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_finding_imports`).Scan(&total); err != nil {
			return pagination.Result[FindingImportHistory]{}, fmt.Errorf("count finding imports: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func readFindingImportRows(fileName string, reader io.Reader) ([][]string, string, error) {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".csv":
		csvReader := csv.NewReader(reader)
		csvReader.FieldsPerRecord = -1
		csvReader.TrimLeadingSpace = true
		rows, err := csvReader.ReadAll()
		return rows, "csv", err
	case ".xlsx":
		file, err := excelize.OpenReader(reader)
		if err != nil {
			return nil, "", err
		}
		defer file.Close()
		sheets := file.GetSheetList()
		if len(sheets) == 0 {
			return nil, "", ErrInvalid
		}
		rows, err := file.GetRows(sheets[0])
		return rows, "xlsx", err
	default:
		return nil, "", ErrInvalid
	}
}

func nonEmptyImportRows(rows [][]string) [][]string {
	result := make([][]string, 0, len(rows))
	for _, row := range rows {
		nonEmpty := false
		for _, value := range row {
			if strings.TrimSpace(value) != "" {
				nonEmpty = true
				break
			}
		}
		if nonEmpty {
			result = append(result, row)
		}
	}
	return result
}

func mapFindingImportHeaders(row []string) map[string]int {
	result := map[string]int{
		"asset_type": -1, "asset_value": -1, "title": -1, "severity": -1, "vulnerability_type": -1,
		"description": -1, "evidence": -1, "remediation_advice": -1, "discovered_at": -1,
	}
	aliases := map[string]string{
		"资产类型": "asset_type", "assettype": "asset_type", "asset_type": "asset_type",
		"资产值": "asset_value", "资产地址": "asset_value", "assetvalue": "asset_value", "asset_value": "asset_value",
		"漏洞标题": "title", "标题": "title", "title": "title",
		"严重等级": "severity", "风险等级": "severity", "severity": "severity",
		"漏洞类型": "vulnerability_type", "vulnerabilitytype": "vulnerability_type", "vulnerability_type": "vulnerability_type",
		"漏洞描述": "description", "描述": "description", "description": "description",
		"验证证据": "evidence", "证据": "evidence", "evidence": "evidence",
		"整改建议": "remediation_advice", "修复建议": "remediation_advice", "remediationadvice": "remediation_advice", "remediation_advice": "remediation_advice",
		"发现时间": "discovered_at", "discoveredat": "discovered_at", "discovered_at": "discovered_at",
	}
	for index, header := range row {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")), " ", ""))
		if key, ok := aliases[normalized]; ok {
			result[key] = index
		}
	}
	return result
}

func findingInputFromImportRow(row []string, headers map[string]int, batchID string) (FindingInput, string, string, *findingImportRowError) {
	value := func(key string) string {
		index := headers[key]
		if index < 0 || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}
	assetType := normalizeFindingAssetType(value("asset_type"))
	assetValue := value("asset_value")
	severity := normalizeFindingSeverity(value("severity"))
	if assetType == "" {
		return FindingInput{}, "", "", &findingImportRowError{"invalid_asset_type", "资产类型仅支持 domain、ip、url、service、other"}
	}
	if assetValue == "" {
		return FindingInput{}, "", "", &findingImportRowError{"asset_value_required", "资产值不能为空"}
	}
	if severity == "" {
		return FindingInput{}, "", "", &findingImportRowError{"invalid_severity", "严重等级仅支持严重、高危、中危、低危、提示，或 critical/high/medium/low/info"}
	}
	discoveredAt, err := parseFindingImportTime(value("discovered_at"))
	if err != nil {
		return FindingInput{}, "", "", &findingImportRowError{"invalid_discovered_at", "发现时间格式不正确，建议使用 YYYY-MM-DD HH:mm:ss"}
	}
	input := FindingInput{
		Title: value("title"), Severity: severity, Status: "open", TestBatchID: batchID,
		VulnerabilityType: value("vulnerability_type"), Description: value("description"), Evidence: value("evidence"),
		RemediationAdvice: value("remediation_advice"), Source: "import", DiscoveredAt: discoveredAt,
	}
	if input.Title == "" {
		return FindingInput{}, "", "", &findingImportRowError{"title_required", "漏洞标题不能为空"}
	}
	if input.Description == "" {
		return FindingInput{}, "", "", &findingImportRowError{"description_required", "漏洞描述不能为空"}
	}
	if len(input.Title) > 255 || len(input.VulnerabilityType) > 128 || len(input.Description) > 20000 || len(input.Evidence) > 30000 || len(input.RemediationAdvice) > 20000 {
		return FindingInput{}, "", "", &findingImportRowError{"field_too_long", "漏洞字段内容超过长度限制"}
	}
	return input, assetType, assetValue, nil
}

func normalizeFindingAssetType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{"域名": "domain", "ip地址": "ip", "地址": "ip", "网址": "url", "链接": "url", "服务": "service", "其他": "other"}
	if normalized, ok := aliases[value]; ok {
		value = normalized
	}
	if value == "domain" || value == "ip" || value == "url" || value == "service" || value == "other" {
		return value
	}
	return ""
}

func normalizeFindingSeverity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{
		"严重": "critical", "严重风险": "critical", "致命": "critical", "critical": "critical",
		"高": "high", "高危": "high", "高风险": "high", "high": "high",
		"中": "medium", "中危": "medium", "中风险": "medium", "medium": "medium",
		"低": "low", "低危": "low", "低风险": "low", "low": "low",
		"提示": "info", "信息": "info", "info": "info",
	}
	return aliases[value]
}

func parseFindingImportTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now(), nil
	}
	if serial, err := strconv.ParseFloat(value, 64); err == nil && serial > 0 {
		return excelize.ExcelDateToTime(serial, false)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006/01/02 15:04:05", "2006/01/02 15:04", "2006-01-02", "2006/01/02"} {
		if layout == time.RFC3339 {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed, nil
			}
			continue
		}
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, ErrInvalid
}
