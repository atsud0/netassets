package securityworkorder

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/findingstatus"
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
	PendingCount   int                  `json:"pending_count"`
	Errors         []FindingImportError `json:"errors"`
}

type FindingImportHistory struct {
	FindingImportResult
	CreatedByName string    `json:"created_by_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type findingImportRowError struct{ code, message string }

type findingImportScope struct {
	ScopeAssetID       string
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
	headers := []string{"资产类型", "资产值", "漏洞标题", "严重等级", "漏洞类型", "漏洞描述", "验证证据", "整改建议", "发现时间", "状态"}
	example := []string{"url", "https://example.com/login", "登录接口未授权访问", "高危", "越权访问", "未登录情况下可以读取用户信息", "GET /api/user/1 返回完整用户信息", "增加服务端身份认证和资源权限校验", "2026-07-22 10:00:00", "pending"}
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
		_ = file.SetColWidth(sheet, "J", "J", 14)
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
	if !principal.CanManageSecurityFindings() {
		return FindingImportResult{}, ErrForbidden
	}
	testBatchID = strings.TrimSpace(testBatchID)
	if testBatchID != "" && !validUUID(testBatchID) {
		return FindingImportResult{}, ErrInvalid
	}
	var batchCode, batchName, batchStatus string
	if testBatchID != "" {
		if err := s.pool.QueryRow(ctx, `SELECT batch_code, batch_name, status FROM security_test_batches WHERE id::text=$1`, testBatchID).Scan(&batchCode, &batchName, &batchStatus); errors.Is(err, pgx.ErrNoRows) {
			return FindingImportResult{}, ErrNotFound
		} else if err != nil {
			return FindingImportResult{}, fmt.Errorf("load finding import batch: %w", err)
		}
		if batchStatus != "authorized" && batchStatus != "in_progress" && batchStatus != "completed" {
			return FindingImportResult{}, ErrState
		}
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

	var scopes []findingImportScope
	if testBatchID != "" {
		scopes, err = s.loadFindingImportScopes(ctx, testBatchID)
	} else {
		scopes, err = s.loadFindingImportAssetScopes(ctx)
	}
	if err != nil {
		return FindingImportResult{}, err
	}
	result := FindingImportResult{
		TestBatchID: testBatchID, TestBatchCode: batchCode, TestBatchName: batchName,
		FileName: filepath.Base(fileName), FileType: fileType, TotalCount: len(rows) - 1,
		Errors: make([]FindingImportError, 0),
	}
	pendingCandidates := make([]pendingFindingImportCandidate, 0)
	for index, row := range rows[1:] {
		rowNumber := index + 2
		input, assetType, assetValue, rowErr := findingInputFromImportRow(row, headers, testBatchID)
		if rowErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: rowErr.code, Message: rowErr.message})
			continue
		}
		normalizedValue, normalizeErr := asset.NormalizeValue(assetType, assetValue)
		if normalizeErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "invalid_asset_value", Message: "资产类型与资产值不匹配，或资产值格式不合法"})
			continue
		}
		matches := exactFindingImportScopes(scopes, assetType, assetValue, normalizedValue)
		if len(matches) != 1 {
			suggestion, matchMethod, confidence := suggestFindingImportScope(scopes, assetType, assetValue, normalizedValue)
			pendingCandidates = append(pendingCandidates, newPendingFindingImportCandidate(
				rowNumber, row, headers, input, assetType, assetValue, normalizedValue, suggestion, matchMethod, confidence,
			))
			result.PendingCount++
			continue
		}
		scope := matches[0]
		input.AssetID = scope.AssetID
		input.AssigneeOrganizationID = scope.OrganizationID
		input.BusinessSystemID = scope.BusinessSystemID

		var duplicate bool
		duplicateQuery := `SELECT EXISTS(SELECT 1 FROM security_findings
			WHERE test_batch_id::text=$1 AND asset_id::text=$2 AND lower(btrim(title))=lower(btrim($3)))`
		duplicateArgs := []any{testBatchID, scope.AssetID, input.Title}
		duplicateMessage := "同一测试批次、资产和漏洞标题的安全发现已存在"
		if testBatchID == "" {
			duplicateQuery = `SELECT EXISTS(SELECT 1 FROM security_findings
				WHERE test_batch_id IS NULL AND asset_id::text=$1 AND lower(btrim(title))=lower(btrim($2)))`
			duplicateArgs = []any{scope.AssetID, input.Title}
			duplicateMessage = "同一资产和漏洞标题的安全发现已存在"
		}
		if err := s.pool.QueryRow(ctx, duplicateQuery, duplicateArgs...).Scan(&duplicate); err != nil {
			return FindingImportResult{}, fmt.Errorf("check duplicate imported finding: %w", err)
		}
		if duplicate {
			result.DuplicateCount++
			result.Errors = append(result.Errors, FindingImportError{Row: rowNumber, Code: "duplicate", Message: duplicateMessage})
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
	if result.SuccessCount == 0 && result.PendingCount == 0 {
		result.Status = "failed"
	} else if result.FailureCount+result.DuplicateCount+result.PendingCount > 0 {
		result.Status = "partial"
	}
	id, err := s.saveFindingImportResult(ctx, principal, result, pendingCandidates, requestID, ipAddress)
	if err != nil {
		return FindingImportResult{}, err
	}
	result.ID = id
	return result, nil
}

func (s *Service) saveFindingImportResult(ctx context.Context, principal auth.Principal, result FindingImportResult, candidates []pendingFindingImportCandidate, requestID, ipAddress string) (string, error) {
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
		(test_batch_id, file_name, file_type, status, total_count, success_count, failure_count, duplicate_count, pending_count, errors, created_by)
		VALUES (NULLIF($1,'')::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::uuid) RETURNING id::text`,
		result.TestBatchID, result.FileName, result.FileType, result.Status, result.TotalCount, result.SuccessCount,
		result.FailureCount, result.DuplicateCount, result.PendingCount, string(errorsJSON), principal.UserID).Scan(&id); err != nil {
		return "", fmt.Errorf("save finding import result: %w", err)
	}
	if err := insertFindingImportCandidates(ctx, tx, id, result.TestBatchID, result.FileName, principal.UserID, candidates); err != nil {
		return "", err
	}
	organizationID := principalOrganizationID(principal)
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, 'security_finding.import', 'security_finding_import', $3,
		'批量导入安全发现', jsonb_build_object('test_batch_id',$4::text,'file_name',$5::text,'total',$6::int,'success',$7::int,'failure',$8::int,'duplicate',$9::int,'pending',$10::int),
		NULLIF($11,'')::inet, NULLIF($12,''))`, principal.UserID, organizationID, id, result.TestBatchID,
		result.FileName, result.TotalCount, result.SuccessCount, result.FailureCount, result.DuplicateCount, result.PendingCount, ipAddress, requestID); err != nil {
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
	if !principal.CanManageSecurityFindings() {
		return pagination.Result[FindingImportHistory]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text, i.test_batch_id::text, b.batch_code, b.batch_name, i.file_name, i.file_type, i.status,
		       i.total_count, i.success_count, i.failure_count, i.duplicate_count, i.pending_count, i.errors::text,
		       COALESCE(u.display_name,''), i.created_at, count(*) OVER ()
		FROM security_finding_imports i
		LEFT JOIN security_test_batches b ON b.id=i.test_batch_id
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
			&item.DuplicateCount, &item.PendingCount, &errorsText, &item.CreatedByName, &item.CreatedAt, &total); err != nil {
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
	case ".json":
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, "", err
		}
		return readFindingImportJSON(data)
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

type findingJSONAsset struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// UnmarshalJSON accepts both the structured asset objects emitted by the
// finding service and the compact string form used by older exports.
func (asset *findingJSONAsset) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		asset.Label = value
		return nil
	}
	var object struct {
		Type  string `json:"type"`
		Label string `json:"label"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	asset.Type = object.Type
	asset.Label = object.Label
	asset.Value = object.Value
	return nil
}

type findingJSONRecord struct {
	ID                string             `json:"id"`
	FindingID         string             `json:"finding_id"`
	Title             string             `json:"title"`
	Name              string             `json:"name"`
	Severity          string             `json:"severity"`
	VulnerabilityType string             `json:"vulnerability_type"`
	VulnClass         string             `json:"vulnclass"`
	Description       string             `json:"description"`
	Summary           string             `json:"summary"`
	Report            string             `json:"report"`
	Evidence          string             `json:"evidence"`
	RemediationAdvice string             `json:"remediation_advice"`
	Remediation       string             `json:"remediation"`
	Status            string             `json:"status"`
	DiscoveredAt      string             `json:"discovered_at"`
	CreatedAt         string             `json:"created_at"`
	Timestamp         string             `json:"ts"`
	Assets            []findingJSONAsset `json:"assets"`
	AssetType         string             `json:"asset_type"`
	AssetValue        string             `json:"asset_value"`
}

// readFindingImportJSON accepts the native security-finding export shape: an
// array of findings with name/severity/summary/evidence/assets fields. One
// row is generated per asset so every exported target can be matched to the
// authorized test scope independently.
func readFindingImportJSON(data []byte) ([][]string, string, error) {
	var records []findingJSONRecord
	parseErr := json.Unmarshal(data, &records)
	if parseErr != nil {
		// Some scanners emit a literal backslash before non-JSON escape
		// characters inside report text. Repair only those invalid escapes,
		// preserving valid JSON escapes, so the import can still consume that
		// export. Valid JSON takes the untouched fast path above.
		data = repairInvalidJSONEscapes(data)
		parseErr = json.Unmarshal(data, &records)
	}
	if parseErr != nil {
		var envelope struct {
			Findings []findingJSONRecord `json:"findings"`
		}
		if envelopeErr := json.Unmarshal(data, &envelope); envelopeErr != nil || len(envelope.Findings) == 0 {
			return nil, "", parseErr
		}
		records = envelope.Findings
	}
	headers := []string{"资产类型", "资产值", "漏洞标题", "严重等级", "漏洞类型", "漏洞描述", "验证证据", "整改建议", "发现时间", "状态"}
	rows := make([][]string, 1, len(records)+1)
	rows[0] = headers
	for _, record := range records {
		title := strings.TrimSpace(record.Title)
		if title == "" {
			title = strings.TrimSpace(record.Name)
		}
		if title == "" {
			identifier := firstNonEmptyImportValue(record.FindingID, record.ID)
			if identifier != "" {
				title = "漏洞发现 #" + identifier
			} else {
				title = "未命名漏洞发现"
			}
		}
		vulnerabilityType := strings.TrimSpace(record.VulnerabilityType)
		if vulnerabilityType == "" {
			vulnerabilityType = strings.TrimSpace(record.VulnClass)
		}
		description := strings.TrimSpace(record.Description)
		if description == "" {
			description = strings.TrimSpace(record.Summary)
		}
		if description == "" {
			description = strings.TrimSpace(record.Report)
		}
		evidence := strings.TrimSpace(record.Evidence)
		advice := strings.TrimSpace(record.RemediationAdvice)
		if advice == "" {
			advice = strings.TrimSpace(record.Remediation)
		}
		discoveredAt := firstNonEmptyImportValue(record.DiscoveredAt, record.CreatedAt, record.Timestamp)
		assets := record.Assets
		if len(assets) == 0 && (record.AssetType != "" || record.AssetValue != "") {
			assets = []findingJSONAsset{{Type: record.AssetType, Value: record.AssetValue}}
		}
		if len(assets) == 0 {
			assets = []findingJSONAsset{{Type: "other", Value: "未提供资产"}}
		}
		for _, item := range assets {
			assetType, assetValue := normalizeFindingJSONAsset(item.Type, firstNonEmptyImportValue(item.Value, item.Label))
			rows = append(rows, []string{assetType, assetValue, title, record.Severity, vulnerabilityType, description, evidence, advice, discoveredAt, strings.TrimSpace(record.Status)})
		}
	}
	if len(rows) < 2 {
		return nil, "", ErrInvalid
	}
	return rows, "json", nil
}

func firstNonEmptyImportValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeFindingJSONAsset(assetType, value string) (string, string) {
	assetType = strings.ToLower(strings.TrimSpace(assetType))
	value = strings.TrimSpace(value)
	switch assetType {
	case "subdomain", "domain", "host":
		return "domain", strings.TrimSuffix(value, ".")
	case "ip", "ip_address", "ipv4", "ipv6":
		return "ip", strings.TrimSpace(strings.Split(value, "/")[0])
	case "endpoint", "url", "uri", "link":
		// Endpoint labels commonly start with "GET "; retain the URL itself.
		parts := strings.Fields(value)
		if len(parts) > 1 && !strings.Contains(parts[0], "://") {
			value = strings.TrimSpace(strings.Join(parts[1:], " "))
		}
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			return "url", value
		}
		return "other", value
	case "service", "port":
		// A scanner "service" row is not always host:port. When the label is a
		// URL the useful identity is the URL itself, and forcing it into the
		// service type made normalizeValue reject the row outright. URL-shaped
		// service rows are therefore kept as url assets instead of being lost.
		if strings.Contains(value, "://") {
			return "url", value
		}
		return "service", value
	default:
		return "other", value
	}
}

func repairInvalidJSONEscapes(data []byte) []byte {
	// Walk strings as a small lexer instead of looking backwards at the output
	// buffer.  Looking backwards is subtly wrong after an invalid escape has
	// been removed: a later quote can then be mistaken for the end of the
	// string.  This is intentionally only a repair fallback; valid JSON is
	// still handled by encoding/json without modification.
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); {
		c := data[i]
		if !inString {
			out = append(out, c)
			i++
			if c == '"' {
				inString = true
			}
			continue
		}

		if c == '"' {
			out = append(out, c)
			i++
			inString = false
			continue
		}
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}

		// Preserve valid JSON escapes as a complete token.  For a malformed
		// escape, drop only the slash and keep scanning the following character
		// as normal report text (for example, \\c.target.example).
		if i+1 >= len(data) {
			i++
			continue
		}
		next := data[i+1]
		switch next {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			out = append(out, c, next)
			i += 2
		case 'u':
			if i+5 < len(data) && isJSONHex(data[i+2]) && isJSONHex(data[i+3]) && isJSONHex(data[i+4]) && isJSONHex(data[i+5]) {
				out = append(out, data[i:i+6]...)
				i += 6
			} else {
				i++
			}
		default:
			i++
		}
	}
	return out
}

func isJSONHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
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
		"description": -1, "evidence": -1, "remediation_advice": -1, "discovered_at": -1, "status": -1,
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
		"状态": "status", "status": "status", "漏洞状态": "status",
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
	status := normalizeFindingImportStatus(value("status"))
	if value("status") != "" && status == "" {
		return FindingInput{}, "", "", &findingImportRowError{"invalid_status", "漏洞状态仅支持 pending、in_progress、confirmed、resolved、fixed、false_positive、ignored、duplicate、risk_accepted，或对应中文标签"}
	}
	discoveredAt, err := parseFindingImportTime(value("discovered_at"))
	if err != nil {
		return FindingInput{}, "", "", &findingImportRowError{"invalid_discovered_at", "发现时间格式不正确，建议使用 YYYY-MM-DD HH:mm:ss"}
	}
	input := FindingInput{
		Title: value("title"), Severity: severity, Status: status, TestBatchID: batchID,
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

// normalizeFindingImportStatus preserves the status semantics emitted by the
// native JSON export. Empty is intentionally pending for old templates that do
// not carry a status column; explicit values are validated against the single
// backend vocabulary instead of being silently reset.
func normalizeFindingImportStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{
		"待处理": findingstatus.Pending, "处理中": findingstatus.InProgress, "已确认": findingstatus.Confirmed,
		"已处理": findingstatus.Resolved, "已修复": findingstatus.Fixed, "误报": findingstatus.FalsePositive,
		"忽略": findingstatus.Ignored, "重复": findingstatus.Duplicate, "风险接受": findingstatus.RiskAccepted,
	}
	if normalized, ok := aliases[value]; ok {
		return normalized
	}
	if value == "" {
		return findingstatus.Pending
	}
	if findingstatus.Valid(value) {
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
