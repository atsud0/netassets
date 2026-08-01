package securityworkorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type pendingFindingImportCandidate struct {
	SourceRowNumber       int
	RawData               map[string]string
	AssetType             string
	AssetValue            string
	NormalizedValue       string
	Input                 FindingInput
	SuggestedScopeAssetID string
	MatchMethod           string
	MatchConfidence       int
}

type FindingImportCandidate struct {
	ID                        string            `json:"id"`
	FindingImportID           string            `json:"finding_import_id"`
	TestBatchID               string            `json:"test_batch_id"`
	TestBatchCode             string            `json:"test_batch_code"`
	TestBatchName             string            `json:"test_batch_name"`
	SourceRowNumber           int               `json:"source_row_number"`
	RawData                   map[string]string `json:"raw_data"`
	AssetType                 string            `json:"asset_type"`
	AssetValue                string            `json:"asset_value"`
	NormalizedValue           string            `json:"normalized_value,omitempty"`
	Title                     string            `json:"title"`
	Severity                  string            `json:"severity"`
	VulnerabilityType         string            `json:"vulnerability_type,omitempty"`
	Description               string            `json:"description"`
	Evidence                  string            `json:"evidence,omitempty"`
	RemediationAdvice         string            `json:"remediation_advice,omitempty"`
	DiscoveredAt              time.Time         `json:"discovered_at"`
	Status                    string            `json:"status"`
	SuggestedScopeAssetID     string            `json:"suggested_scope_asset_id,omitempty"`
	SuggestedAssetValue       string            `json:"suggested_asset_value,omitempty"`
	SuggestedOrganizationName string            `json:"suggested_organization_name,omitempty"`
	MatchedScopeAssetID       string            `json:"matched_scope_asset_id,omitempty"`
	MatchedAssetValue         string            `json:"matched_asset_value,omitempty"`
	MatchedOrganizationName   string            `json:"matched_organization_name,omitempty"`
	MatchMethod               string            `json:"match_method,omitempty"`
	MatchConfidence           int               `json:"match_confidence,omitempty"`
	ReviewNote                string            `json:"review_note,omitempty"`
	ReviewedByName            string            `json:"reviewed_by_name,omitempty"`
	ReviewedAt                *time.Time        `json:"reviewed_at,omitempty"`
	ImportedFindingID         string            `json:"imported_finding_id,omitempty"`
	ImportedFindingCode       string            `json:"imported_finding_code,omitempty"`
	CreatedAt                 time.Time         `json:"created_at"`
	UpdatedAt                 time.Time         `json:"updated_at"`
}

type FindingImportCandidateFilter struct {
	TestBatchID string
	Status      string
	AssetType   string
	Keyword     string
	Page        pagination.Query
}

type FindingImportCandidateLinkInput struct {
	ScopeAssetID string `json:"scope_asset_id"`
	ReviewNote   string `json:"review_note"`
}

type FindingImportCandidateReviewInput struct {
	ReviewNote string `json:"review_note"`
}

type FindingImportScopeOption struct {
	ID                 string `json:"id"`
	AssetID            string `json:"asset_id"`
	AssetType          string `json:"asset_type"`
	AssetValue         string `json:"asset_value"`
	AssetName          string `json:"asset_name,omitempty"`
	OrganizationID     string `json:"organization_id"`
	OrganizationCode   string `json:"organization_code"`
	OrganizationName   string `json:"organization_name"`
	BusinessSystemID   string `json:"business_system_id,omitempty"`
	BusinessSystemCode string `json:"business_system_code,omitempty"`
	BusinessSystemName string `json:"business_system_name,omitempty"`
	Suggested          bool   `json:"suggested"`
}

func (s *Service) loadFindingImportScopes(ctx context.Context, batchID string) ([]findingImportScope, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT scope.id::text, scope.asset_id::text, scope.organization_id::text,
		       COALESCE(scope.business_system_id::text, ''), scope.asset_type_snapshot,
		       scope.asset_value_snapshot, scope.organization_name_snapshot,
		       COALESCE(scope.business_system_name_snapshot, '')
		FROM security_test_scope_assets scope
		JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
		JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
		WHERE scope.batch_id::text=$1
		ORDER BY scope.created_at`, batchID)
	if err != nil {
		return nil, fmt.Errorf("load finding import scopes: %w", err)
	}
	defer rows.Close()
	items := make([]findingImportScope, 0)
	for rows.Next() {
		var item findingImportScope
		if err := rows.Scan(&item.ScopeAssetID, &item.AssetID, &item.OrganizationID, &item.BusinessSystemID,
			&item.AssetTypeSnapshot, &item.AssetValueSnapshot, &item.OrganizationName, &item.BusinessSystemName); err != nil {
			return nil, fmt.Errorf("scan finding import scope: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finding import scopes: %w", err)
	}
	return items, nil
}

func exactFindingImportScopes(scopes []findingImportScope, assetType, assetValue, normalizedValue string) []findingImportScope {
	matches := make([]findingImportScope, 0, 1)
	for _, scope := range scopes {
		if !strings.EqualFold(scope.AssetTypeSnapshot, assetType) {
			continue
		}
		scopeNormalized, err := asset.NormalizeValue(strings.ToLower(scope.AssetTypeSnapshot), scope.AssetValueSnapshot)
		if normalizedValue != "" && err == nil && scopeNormalized == normalizedValue {
			matches = append(matches, scope)
			continue
		}
		if normalizedValue == "" && strings.EqualFold(strings.TrimSpace(scope.AssetValueSnapshot), strings.TrimSpace(assetValue)) {
			matches = append(matches, scope)
		}
	}
	return matches
}

func suggestFindingImportScope(scopes []findingImportScope, assetType, assetValue, normalizedValue string) (findingImportScope, string, int) {
	type scoredScope struct {
		scope      findingImportScope
		method     string
		confidence int
	}
	scored := make([]scoredScope, 0)
	for _, scope := range scopes {
		method, confidence := scoreFindingImportScope(scope, assetType, assetValue, normalizedValue)
		if confidence > 0 {
			scored = append(scored, scoredScope{scope: scope, method: method, confidence: confidence})
		}
	}
	if len(scored) == 0 {
		return findingImportScope{}, "", 0
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].confidence > scored[j].confidence })
	if len(scored) > 1 && scored[0].confidence == scored[1].confidence {
		return findingImportScope{}, scored[0].method, scored[0].confidence
	}
	return scored[0].scope, scored[0].method, scored[0].confidence
}

func scoreFindingImportScope(scope findingImportScope, assetType, assetValue, normalizedValue string) (string, int) {
	scopeType := strings.ToLower(scope.AssetTypeSnapshot)
	scopeValue := strings.TrimSpace(scope.AssetValueSnapshot)
	scopeNormalized, _ := asset.NormalizeValue(scopeType, scopeValue)
	if strings.EqualFold(scopeType, assetType) && normalizedValue != "" && scopeNormalized == normalizedValue {
		return "normalized_exact", 100
	}
	if host := importedEndpointHost(assetValue); host != "" && scopeType == "ip" && scopeNormalized == host {
		return "endpoint_host", 90
	}
	if _, network, err := net.ParseCIDR(strings.TrimSpace(assetValue)); err == nil && scopeType == "ip" {
		if ip := net.ParseIP(scopeNormalized); ip != nil && network.Contains(ip) {
			return "cidr_contains", 70
		}
	}
	incomingHost := importedHost(assetType, assetValue)
	scopeHost := importedHost(scopeType, scopeValue)
	if incomingHost != "" && scopeHost != "" && incomingHost == scopeHost {
		if assetType == scopeType {
			return "same_host", 80
		}
		return "cross_type_host", 75
	}
	wildcard := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(assetValue), ".")), "*.")
	if strings.HasPrefix(strings.TrimSpace(assetValue), "*.") && scopeHost != "" && (scopeHost == wildcard || strings.HasSuffix(scopeHost, "."+wildcard)) {
		return "wildcard_domain", 65
	}
	return "", 0
}

func importedEndpointHost(value string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port == "" {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func importedHost(assetType, value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(assetType) {
	case "domain":
		return strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(value, ".")), "*.")
	case "url":
		parsed, err := url.Parse(value)
		if err == nil {
			return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		}
	case "ip":
		if ip := net.ParseIP(value); ip != nil {
			return ip.String()
		}
		return importedEndpointHost(value)
	case "service":
		host, _, err := net.SplitHostPort(value)
		if err == nil {
			host = strings.Trim(host, "[]")
			if ip := net.ParseIP(host); ip != nil {
				return ip.String()
			}
			return strings.ToLower(strings.TrimSuffix(host, "."))
		}
	}
	return ""
}

func newPendingFindingImportCandidate(rowNumber int, row []string, headers map[string]int, input FindingInput, assetType, assetValue, normalizedValue string, suggestion findingImportScope, method string, confidence int) pendingFindingImportCandidate {
	rawData := make(map[string]string)
	for key, index := range headers {
		if index >= 0 && index < len(row) {
			rawData[key] = strings.TrimSpace(row[index])
		}
	}
	return pendingFindingImportCandidate{
		SourceRowNumber: rowNumber, RawData: rawData, AssetType: assetType, AssetValue: assetValue,
		NormalizedValue: normalizedValue, Input: input, SuggestedScopeAssetID: suggestion.ScopeAssetID,
		MatchMethod: method, MatchConfidence: confidence,
	}
}

func insertFindingImportCandidates(ctx context.Context, tx pgx.Tx, importID, batchID string, candidates []pendingFindingImportCandidate) error {
	for _, candidate := range candidates {
		rawData, err := json.Marshal(candidate.RawData)
		if err != nil {
			return fmt.Errorf("encode finding import candidate raw data: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO security_finding_import_candidates (
				finding_import_id, test_batch_id, source_row_number, raw_data, asset_type, asset_value,
				normalized_value, title, severity, vulnerability_type, description, evidence,
				remediation_advice, discovered_at, status, suggested_scope_asset_id, match_method, match_confidence
			) VALUES ($1::uuid,$2::uuid,$3,$4::jsonb,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,''),$11,
			          NULLIF($12,''),NULLIF($13,''),$14,'pending',NULLIF($15,'')::uuid,NULLIF($16,''),NULLIF($17,0))`,
			importID, batchID, candidate.SourceRowNumber, string(rawData), candidate.AssetType, candidate.AssetValue,
			candidate.NormalizedValue, candidate.Input.Title, candidate.Input.Severity, candidate.Input.VulnerabilityType,
			candidate.Input.Description, candidate.Input.Evidence, candidate.Input.RemediationAdvice, candidate.Input.DiscoveredAt,
			candidate.SuggestedScopeAssetID, candidate.MatchMethod, candidate.MatchConfidence); err != nil {
			return fmt.Errorf("insert finding import candidate: %w", err)
		}
	}
	return nil
}

func validFindingImportCandidateStatus(status string) bool {
	switch status {
	case "pending", "imported", "ignored", "invalid", "duplicate":
		return true
	default:
		return false
	}
}

func (s *Service) SearchFindingImportCandidates(ctx context.Context, principal auth.Principal, filter FindingImportCandidateFilter) (pagination.Result[FindingImportCandidate], error) {
	if !principal.CanManageSecurityFindings() {
		return pagination.Result[FindingImportCandidate]{}, ErrForbidden
	}
	filter.TestBatchID = strings.TrimSpace(filter.TestBatchID)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.AssetType = strings.TrimSpace(filter.AssetType)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	if (filter.TestBatchID != "" && !validUUID(filter.TestBatchID)) ||
		(filter.Status != "" && !validFindingImportCandidateStatus(filter.Status)) ||
		(filter.AssetType != "" && normalizeFindingAssetType(filter.AssetType) == "") || len(filter.Keyword) > 200 {
		return pagination.Result[FindingImportCandidate]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, findingImportCandidateSelect+`
		WHERE ($1::text='' OR c.test_batch_id::text=$1)
		  AND ($2::text='' OR c.status=$2)
		  AND ($3::text='' OR c.asset_type=$3)
		  AND ($4::text='' OR c.asset_value ILIKE '%' || $4 || '%' OR c.title ILIKE '%' || $4 || '%')
		ORDER BY CASE WHEN c.status='pending' THEN 0 ELSE 1 END, c.created_at DESC
		LIMIT $5 OFFSET $6`, filter.TestBatchID, filter.Status, filter.AssetType, filter.Keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[FindingImportCandidate]{}, fmt.Errorf("search finding import candidates: %w", err)
	}
	defer rows.Close()
	items := make([]FindingImportCandidate, 0, filter.Page.PageSize)
	total := 0
	for rows.Next() {
		item, rowTotal, err := scanFindingImportCandidate(rows, true)
		if err != nil {
			return pagination.Result[FindingImportCandidate]{}, err
		}
		total = rowTotal
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[FindingImportCandidate]{}, fmt.Errorf("iterate finding import candidates: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), filter.Page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_finding_import_candidates c
			WHERE ($1::text='' OR c.test_batch_id::text=$1) AND ($2::text='' OR c.status=$2)
			  AND ($3::text='' OR c.asset_type=$3)
			  AND ($4::text='' OR c.asset_value ILIKE '%' || $4 || '%' OR c.title ILIKE '%' || $4 || '%')`,
			filter.TestBatchID, filter.Status, filter.AssetType, filter.Keyword).Scan(&total); err != nil {
			return pagination.Result[FindingImportCandidate]{}, fmt.Errorf("count finding import candidates: %w", err)
		}
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

const findingImportCandidateSelect = `
	SELECT c.id::text, c.finding_import_id::text, c.test_batch_id::text, b.batch_code, b.batch_name,
	       c.source_row_number, c.raw_data::text, c.asset_type, c.asset_value, COALESCE(c.normalized_value,''),
	       c.title, c.severity, COALESCE(c.vulnerability_type,''), c.description, COALESCE(c.evidence,''),
	       COALESCE(c.remediation_advice,''), c.discovered_at, c.status,
	       COALESCE(c.suggested_scope_asset_id::text,''), COALESCE(suggested.asset_value_snapshot,''),
	       COALESCE(suggested.organization_name_snapshot,''), COALESCE(c.matched_scope_asset_id::text,''),
	       COALESCE(matched.asset_value_snapshot,''), COALESCE(matched.organization_name_snapshot,''),
	       COALESCE(c.match_method,''), COALESCE(c.match_confidence,0), COALESCE(c.review_note,''),
	       COALESCE(reviewer.display_name,''), c.reviewed_at, COALESCE(c.imported_finding_id::text,''),
	       COALESCE(f.finding_code,''), c.created_at, c.updated_at, count(*) OVER ()
	FROM security_finding_import_candidates c
	JOIN security_test_batches b ON b.id=c.test_batch_id
	LEFT JOIN security_test_scope_assets suggested ON suggested.id=c.suggested_scope_asset_id
	LEFT JOIN security_test_scope_assets matched ON matched.id=c.matched_scope_asset_id
	LEFT JOIN users reviewer ON reviewer.id=c.reviewed_by
	LEFT JOIN security_findings f ON f.id=c.imported_finding_id`

func scanFindingImportCandidate(row pgx.Row, withTotal bool) (FindingImportCandidate, int, error) {
	var item FindingImportCandidate
	var rawDataText string
	total := 0
	values := []any{
		&item.ID, &item.FindingImportID, &item.TestBatchID, &item.TestBatchCode, &item.TestBatchName,
		&item.SourceRowNumber, &rawDataText, &item.AssetType, &item.AssetValue, &item.NormalizedValue,
		&item.Title, &item.Severity, &item.VulnerabilityType, &item.Description, &item.Evidence,
		&item.RemediationAdvice, &item.DiscoveredAt, &item.Status, &item.SuggestedScopeAssetID,
		&item.SuggestedAssetValue, &item.SuggestedOrganizationName, &item.MatchedScopeAssetID,
		&item.MatchedAssetValue, &item.MatchedOrganizationName, &item.MatchMethod, &item.MatchConfidence,
		&item.ReviewNote, &item.ReviewedByName, &item.ReviewedAt, &item.ImportedFindingID,
		&item.ImportedFindingCode, &item.CreatedAt, &item.UpdatedAt,
	}
	if withTotal {
		values = append(values, &total)
	}
	if err := row.Scan(values...); err != nil {
		return FindingImportCandidate{}, 0, fmt.Errorf("scan finding import candidate: %w", err)
	}
	item.RawData = make(map[string]string)
	if err := json.Unmarshal([]byte(rawDataText), &item.RawData); err != nil {
		return FindingImportCandidate{}, 0, fmt.Errorf("decode finding import candidate raw data: %w", err)
	}
	return item, total, nil
}

func (s *Service) GetFindingImportCandidate(ctx context.Context, principal auth.Principal, id string) (FindingImportCandidate, error) {
	if !principal.CanManageSecurityFindings() {
		return FindingImportCandidate{}, ErrForbidden
	}
	if !validUUID(strings.TrimSpace(id)) {
		return FindingImportCandidate{}, ErrInvalid
	}
	selectOne := strings.Replace(findingImportCandidateSelect, ", count(*) OVER ()", "", 1)
	item, _, err := scanFindingImportCandidate(s.pool.QueryRow(ctx, selectOne+` WHERE c.id=$1::uuid`, id), false)
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	}
	return item, err
}

func (s *Service) ListFindingImportScopeOptions(ctx context.Context, principal auth.Principal, candidateID string) ([]FindingImportScopeOption, error) {
	if !principal.CanManageSecurityFindings() {
		return nil, ErrForbidden
	}
	if !validUUID(strings.TrimSpace(candidateID)) {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `
		SELECT scope.id::text, scope.asset_id::text, scope.asset_type_snapshot, scope.asset_value_snapshot,
		       COALESCE(scope.asset_name_snapshot,''), scope.organization_id::text, scope.organization_code_snapshot,
		       scope.organization_name_snapshot, COALESCE(scope.business_system_id::text,''),
		       COALESCE(scope.business_system_code_snapshot,''), COALESCE(scope.business_system_name_snapshot,''),
		       scope.id=c.suggested_scope_asset_id
		FROM security_finding_import_candidates c
		JOIN security_test_scope_assets scope ON scope.batch_id=c.test_batch_id
		JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
		JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
		WHERE c.id=$1::uuid
		ORDER BY scope.id=c.suggested_scope_asset_id DESC, scope.organization_name_snapshot,
		         scope.asset_type_snapshot, scope.asset_value_snapshot`, candidateID)
	if err != nil {
		return nil, fmt.Errorf("list finding import scope options: %w", err)
	}
	defer rows.Close()
	items := make([]FindingImportScopeOption, 0)
	for rows.Next() {
		var item FindingImportScopeOption
		if err := rows.Scan(&item.ID, &item.AssetID, &item.AssetType, &item.AssetValue, &item.AssetName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.BusinessSystemID,
			&item.BusinessSystemCode, &item.BusinessSystemName, &item.Suggested); err != nil {
			return nil, fmt.Errorf("scan finding import scope option: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finding import scope options: %w", err)
	}
	if len(items) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM security_finding_import_candidates WHERE id=$1::uuid)`, candidateID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check finding import candidate: %w", err)
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (s *Service) LinkFindingImportCandidate(ctx context.Context, principal auth.Principal, id string, input FindingImportCandidateLinkInput, requestID, ipAddress string) (FindingImportCandidate, error) {
	if !principal.CanManageSecurityFindings() {
		return FindingImportCandidate{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	input.ScopeAssetID = strings.TrimSpace(input.ScopeAssetID)
	input.ReviewNote = strings.TrimSpace(input.ReviewNote)
	if !validUUID(id) || !validUUID(input.ScopeAssetID) || len(input.ReviewNote) > 2000 {
		return FindingImportCandidate{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("begin link finding import candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var candidate FindingImportCandidate
	if err := tx.QueryRow(ctx, `
		SELECT finding_import_id::text, test_batch_id::text, title, severity, COALESCE(vulnerability_type,''),
		       description, COALESCE(evidence,''), COALESCE(remediation_advice,''), discovered_at, status
		FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(
		&candidate.FindingImportID, &candidate.TestBatchID, &candidate.Title, &candidate.Severity,
		&candidate.VulnerabilityType, &candidate.Description, &candidate.Evidence, &candidate.RemediationAdvice,
		&candidate.DiscoveredAt, &candidate.Status); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("lock finding import candidate: %w", err)
	}
	if candidate.Status != "pending" {
		return FindingImportCandidate{}, ErrState
	}
	var scope findingImportScope
	if err := tx.QueryRow(ctx, `
		SELECT scope.id::text, scope.asset_id::text, scope.organization_id::text,
		       COALESCE(scope.business_system_id::text,''), scope.asset_type_snapshot,
		       scope.asset_value_snapshot, scope.organization_name_snapshot,
		       COALESCE(scope.business_system_name_snapshot,'')
		FROM security_test_scope_assets scope
		JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
		JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
		WHERE scope.id=$1::uuid AND scope.batch_id=$2::uuid`, input.ScopeAssetID, candidate.TestBatchID).Scan(
		&scope.ScopeAssetID, &scope.AssetID, &scope.OrganizationID, &scope.BusinessSystemID,
		&scope.AssetTypeSnapshot, &scope.AssetValueSnapshot, &scope.OrganizationName, &scope.BusinessSystemName); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("load selected finding import scope: %w", err)
	}
	var duplicateFindingID string
	duplicateErr := tx.QueryRow(ctx, `
		SELECT id::text FROM security_findings
		WHERE test_batch_id=$1::uuid AND asset_id=$2::uuid AND lower(btrim(title))=lower(btrim($3))
		ORDER BY created_at LIMIT 1`, candidate.TestBatchID, scope.AssetID, candidate.Title).Scan(&duplicateFindingID)
	if duplicateErr != nil && !errors.Is(duplicateErr, pgx.ErrNoRows) {
		return FindingImportCandidate{}, fmt.Errorf("check linked finding duplicate: %w", duplicateErr)
	}
	status := "imported"
	findingID := duplicateFindingID
	if errors.Is(duplicateErr, pgx.ErrNoRows) {
		findingInput := FindingInput{
			Title: candidate.Title, Severity: candidate.Severity, Status: "open",
			AssigneeOrganizationID: scope.OrganizationID, AssetID: scope.AssetID,
			BusinessSystemID: scope.BusinessSystemID, TestBatchID: candidate.TestBatchID,
			VulnerabilityType: candidate.VulnerabilityType, Description: candidate.Description,
			Evidence: candidate.Evidence, RemediationAdvice: candidate.RemediationAdvice,
			Source: "import", DiscoveredAt: candidate.DiscoveredAt,
		}
		findingID, _, err = s.createFindingInTx(ctx, tx, principal, findingInput, requestID, ipAddress)
		if err != nil {
			return FindingImportCandidate{}, err
		}
	} else {
		status = "duplicate"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_finding_import_candidates
		SET status=$2, matched_scope_asset_id=$3::uuid, review_note=NULLIF($4,''), reviewed_by=$5::uuid,
		    reviewed_at=now(), imported_finding_id=$6::uuid, updated_at=now()
		WHERE id=$1::uuid`, id, status, scope.ScopeAssetID, input.ReviewNote, principal.UserID, findingID); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("update linked finding import candidate: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE security_finding_imports SET pending_count=GREATEST(pending_count-1,0) WHERE id=$1::uuid`, candidate.FindingImportID); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("update finding import pending count: %w", err)
	}
	action := "security_finding_import_candidate.imported"
	summary := "人工关联漏洞导入项并生成安全发现"
	if status == "duplicate" {
		action = "security_finding_import_candidate.duplicate"
		summary = "人工关联漏洞导入项并标记为重复"
	}
	if err := writeAudit(ctx, tx, principal, scope.OrganizationID, action, "security_finding_import_candidate", id,
		summary, map[string]any{"test_batch_id": candidate.TestBatchID, "scope_asset_id": scope.ScopeAssetID, "asset_id": scope.AssetID, "finding_id": findingID, "review_note": input.ReviewNote}, requestID, ipAddress); err != nil {
		return FindingImportCandidate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("commit link finding import candidate: %w", err)
	}
	return s.GetFindingImportCandidate(ctx, principal, id)
}

func (s *Service) IgnoreFindingImportCandidate(ctx context.Context, principal auth.Principal, id string, input FindingImportCandidateReviewInput, requestID, ipAddress string) (FindingImportCandidate, error) {
	if !principal.CanManageSecurityFindings() {
		return FindingImportCandidate{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	input.ReviewNote = strings.TrimSpace(input.ReviewNote)
	if !validUUID(id) || input.ReviewNote == "" || len(input.ReviewNote) > 2000 {
		return FindingImportCandidate{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("begin ignore finding import candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var importID, batchID, status string
	if err := tx.QueryRow(ctx, `SELECT finding_import_id::text,test_batch_id::text,status FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(&importID, &batchID, &status); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("lock ignored finding import candidate: %w", err)
	}
	if status != "pending" {
		return FindingImportCandidate{}, ErrState
	}
	if _, err := tx.Exec(ctx, `UPDATE security_finding_import_candidates SET status='ignored',review_note=$2,reviewed_by=$3::uuid,reviewed_at=now(),updated_at=now() WHERE id=$1::uuid`, id, input.ReviewNote, principal.UserID); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("ignore finding import candidate: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE security_finding_imports SET pending_count=GREATEST(pending_count-1,0) WHERE id=$1::uuid`, importID); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("update ignored finding import pending count: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, "", "security_finding_import_candidate.ignored", "security_finding_import_candidate", id,
		"忽略漏洞导入待关联项", map[string]any{"test_batch_id": batchID, "review_note": input.ReviewNote}, requestID, ipAddress); err != nil {
		return FindingImportCandidate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FindingImportCandidate{}, fmt.Errorf("commit ignore finding import candidate: %w", err)
	}
	return s.GetFindingImportCandidate(ctx, principal, id)
}
