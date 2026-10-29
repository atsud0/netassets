package securityworkorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"netassets/internal/access"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/findingstatus"
	"netassets/internal/pagination"
	"netassets/internal/textlength"
)

type pendingFindingImportCandidate struct {
	SourceRowNumber       int
	RawData               map[string]string
	AssetType             string
	AssetValue            string
	NormalizedValue       string
	Input                 FindingInput
	SuggestedScopeAssetID string
	SuggestedAssetID      string
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
	ImportStatusRaw           string            `json:"import_status_raw,omitempty"`
	ImportStatusNormalized    string            `json:"import_status_normalized,omitempty"`
	ImportStatusMappingReason string            `json:"import_status_mapping_reason,omitempty"`
	AssetCandidateID          string            `json:"asset_candidate_id,omitempty"`
	SuggestedScopeAssetID     string            `json:"suggested_scope_asset_id,omitempty"`
	SuggestedAssetID          string            `json:"suggested_asset_id,omitempty"`
	SuggestedAssetValue       string            `json:"suggested_asset_value,omitempty"`
	SuggestedOrganizationName string            `json:"suggested_organization_name,omitempty"`
	MatchedScopeAssetID       string            `json:"matched_scope_asset_id,omitempty"`
	MatchedAssetID            string            `json:"matched_asset_id,omitempty"`
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
	AssetID      string `json:"asset_id"`
	ReviewNote   string `json:"review_note"`
	// AssetCandidateID and PendingAssetDraftID let a release target an
	// unpublished organization working version instead of a formal asset.
	AssetCandidateID    string `json:"asset_candidate_id,omitempty"`
	PendingAssetDraftID string `json:"pending_asset_draft_id,omitempty"`
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

func (s *Service) loadFindingImportAssetScopes(ctx context.Context) ([]findingImportScope, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text, a.id::text, owner.organization_id::text,
		       COALESCE(system.business_system_id::text, ''), a.asset_type,
		       a.asset_value, owner.organization_name,
		       COALESCE(system.business_system_name, '')
		FROM assets a
		JOIN LATERAL (
			SELECT ao.organization_id, o.organization_name
			FROM asset_organizations ao
			JOIN organizations o ON o.id=ao.organization_id
			WHERE ao.asset_id=a.id AND o.status='enabled' AND o.deleted_at IS NULL
			ORDER BY ao.is_primary DESC, (ao.relationship_type='owner') DESC, ao.created_at
			LIMIT 1
		) owner ON true
		LEFT JOIN LATERAL (
			SELECT sa.business_system_id, bs.system_name AS business_system_name
			FROM system_assets sa
			JOIN business_systems bs ON bs.id=sa.business_system_id AND bs.deleted_at IS NULL
			WHERE sa.asset_id=a.id
			ORDER BY sa.is_primary DESC, sa.created_at
			LIMIT 1
		) system ON true
		WHERE a.deleted_at IS NULL
		ORDER BY a.created_at`)
	if err != nil {
		return nil, fmt.Errorf("load finding import assets: %w", err)
	}
	defer rows.Close()
	items := make([]findingImportScope, 0)
	for rows.Next() {
		var item findingImportScope
		if err := rows.Scan(&item.ScopeAssetID, &item.AssetID, &item.OrganizationID, &item.BusinessSystemID,
			&item.AssetTypeSnapshot, &item.AssetValueSnapshot, &item.OrganizationName, &item.BusinessSystemName); err != nil {
			return nil, fmt.Errorf("scan finding import asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finding import assets: %w", err)
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
		SuggestedAssetID: suggestion.AssetID, MatchMethod: method, MatchConfidence: confidence,
	}
}

func insertFindingImportCandidates(ctx context.Context, tx pgx.Tx, importID, batchID, fileName, createdBy string, candidates []pendingFindingImportCandidate) error {
	for _, candidate := range candidates {
		rawData, err := json.Marshal(candidate.RawData)
		if err != nil {
			return fmt.Errorf("encode finding import candidate raw data: %w", err)
		}

		// Keep one asset discovery for the same missing asset even when a file
		// contains several vulnerabilities for it. The finding candidates below
		// all point to that discovery and are released together when the asset is
		// confirmed.
		var assetCandidateID string
		if batchID == "" && candidate.SuggestedAssetID == "" && candidate.NormalizedValue != "" {
			err = tx.QueryRow(ctx, `
			SELECT id::text FROM asset_candidates
			WHERE asset_type=$1 AND normalized_value=$2 AND status='pending'
			ORDER BY created_at LIMIT 1`, candidate.AssetType, candidate.NormalizedValue).Scan(&assetCandidateID)
			if errors.Is(err, pgx.ErrNoRows) {
				var suggestedData map[string]any
				if err := json.Unmarshal(rawData, &suggestedData); err != nil {
					return fmt.Errorf("decode finding import raw data: %w", err)
				}
				if suggestedData == nil {
					suggestedData = map[string]any{}
				}
				suggestedData["source"] = "security_finding_import"
				suggestedData["finding_import_id"] = importID
				suggestedData["finding_title"] = candidate.Input.Title
				suggestedData["severity"] = candidate.Input.Severity
				suggestedData["vulnerability_type"] = candidate.Input.VulnerabilityType
				suggestedData["description"] = candidate.Input.Description
				suggestedData["evidence"] = candidate.Input.Evidence
				suggestedData["discovered_at"] = candidate.Input.DiscoveredAt.Format(time.RFC3339)
				suggestedData["organization_pending"] = true
				suggested, err := json.Marshal(suggestedData)
				if err != nil {
					return fmt.Errorf("encode finding asset candidate data: %w", err)
				}
				var sourceReference = "漏洞导入：" + filepath.Base(fileName)
				if len(sourceReference) > 255 {
					sourceReference = sourceReference[:255]
				}
				err = tx.QueryRow(ctx, `
				INSERT INTO asset_candidates
					(asset_type, asset_value, normalized_value, suggested_data, organization_id,
					 source, source_reference, status, created_by)
				VALUES ($1,$2,$3,$4::jsonb,NULL,'finding_import',NULLIF($5,''),'pending',$6::uuid)
				ON CONFLICT (asset_type, normalized_value) WHERE status='pending' AND normalized_value <> ''
				DO UPDATE SET updated_at=now()
				RETURNING id::text`, candidate.AssetType, candidate.AssetValue, candidate.NormalizedValue,
					suggested, sourceReference, createdBy).Scan(&assetCandidateID)
				if err != nil {
					return fmt.Errorf("insert finding asset candidate: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("find existing asset candidate: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO security_finding_import_candidates (
				finding_import_id, test_batch_id, source_row_number, raw_data, asset_type, asset_value,
				normalized_value, title, severity, vulnerability_type, description, evidence,
				remediation_advice, discovered_at, status, import_status_raw, import_status_normalized,
				import_status_mapping_reason, suggested_scope_asset_id, suggested_asset_id, asset_candidate_id,
				match_method, match_confidence
			) VALUES ($1::uuid,NULLIF($2,'')::uuid,$3,$4::jsonb,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,''),$11,
			          NULLIF($12,''),NULLIF($13,''),$14,'pending',NULLIF($15,''),NULLIF($16,''),NULLIF($17,''),
		          NULLIF($18,'')::uuid,NULLIF($19,'')::uuid,NULLIF($20,'')::uuid,NULLIF($21,''),NULLIF($22,0))`,
			importID, batchID, candidate.SourceRowNumber, string(rawData), candidate.AssetType, candidate.AssetValue,
			candidate.NormalizedValue, candidate.Input.Title, candidate.Input.Severity, candidate.Input.VulnerabilityType,
			candidate.Input.Description, candidate.Input.Evidence, candidate.Input.RemediationAdvice, candidate.Input.DiscoveredAt,
			candidate.Input.ImportStatusRaw, candidate.Input.ImportStatusNormalized, candidate.Input.ImportStatusMappingReason,
			candidate.SuggestedScopeAssetID, candidate.SuggestedAssetID, assetCandidateID, candidate.MatchMethod, candidate.MatchConfidence); err != nil {
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
	if filter.TestBatchID != "" && !validUUID(filter.TestBatchID) {
		return pagination.Result[FindingImportCandidate]{}, invalidField("安全测试批次标识不正确")
	}
	if filter.Status != "" && !validFindingImportCandidateStatus(filter.Status) {
		return pagination.Result[FindingImportCandidate]{}, invalidField("请选择有效的导入候选状态")
	}
	if filter.AssetType != "" && normalizeFindingAssetType(filter.AssetType) == "" {
		return pagination.Result[FindingImportCandidate]{}, invalidField("请选择有效的资产类型")
	}
	if textlength.Count(filter.Keyword) > 200 {
		return pagination.Result[FindingImportCandidate]{}, invalidField("搜索关键词最多 200 字")
	}
	rows, err := s.pool.Query(ctx, findingImportCandidateSelect+`
		WHERE ($1::text='' OR c.test_batch_id::text=$1)
		  AND ($2::text='' OR c.status=$2)
		  AND ($3::text='' OR c.asset_type=$3)
		  AND ($4::text='' OR c.asset_value ILIKE '%' || $4 || '%' OR c.title ILIKE '%' || $4 || '%')
		ORDER BY CASE WHEN c.status='pending' THEN 0 ELSE 1 END, c.created_at DESC, c.id DESC
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
	SELECT c.id::text, c.finding_import_id::text, COALESCE(c.test_batch_id::text,''), COALESCE(b.batch_code,''), COALESCE(b.batch_name,''),
	       c.source_row_number, c.raw_data::text, c.asset_type, c.asset_value, COALESCE(c.normalized_value,''),
	       c.title, c.severity, COALESCE(c.vulnerability_type,''), c.description, COALESCE(c.evidence,''),
	       COALESCE(c.remediation_advice,''), c.discovered_at, c.status,
	       COALESCE(c.import_status_raw,''), COALESCE(c.import_status_normalized,''), COALESCE(c.import_status_mapping_reason,''),
	       COALESCE(c.suggested_scope_asset_id::text,''), COALESCE(c.suggested_asset_id::text,''),
	       COALESCE(suggested.asset_value_snapshot, suggested_asset.asset_value,''),
	       COALESCE(suggested.organization_name_snapshot, suggested_org.organization_name,''),
	       COALESCE(c.matched_scope_asset_id::text,''), COALESCE(c.matched_asset_id::text,''),
	       COALESCE(matched.asset_value_snapshot, matched_asset.asset_value,''),
	       COALESCE(matched.organization_name_snapshot, matched_org.organization_name,''),
	       COALESCE(c.match_method,''), COALESCE(c.match_confidence,0), COALESCE(c.review_note,''),
	       COALESCE(reviewer.display_name,''), c.reviewed_at, COALESCE(c.imported_finding_id::text,''),
	       COALESCE(f.finding_code,''), COALESCE(c.asset_candidate_id::text,''), c.created_at, c.updated_at, count(*) OVER ()
	FROM security_finding_import_candidates c
	LEFT JOIN security_test_batches b ON b.id=c.test_batch_id
	LEFT JOIN security_test_scope_assets suggested ON suggested.id=c.suggested_scope_asset_id
	LEFT JOIN assets suggested_asset ON suggested_asset.id=c.suggested_asset_id
	LEFT JOIN organizations suggested_org ON suggested_org.id=(SELECT ao.organization_id FROM asset_organizations ao WHERE ao.asset_id=suggested_asset.id ORDER BY ao.is_primary DESC, (ao.relationship_type='owner') DESC LIMIT 1)
	LEFT JOIN security_test_scope_assets matched ON matched.id=c.matched_scope_asset_id
	LEFT JOIN assets matched_asset ON matched_asset.id=c.matched_asset_id
	LEFT JOIN organizations matched_org ON matched_org.id=(SELECT ao.organization_id FROM asset_organizations ao WHERE ao.asset_id=matched_asset.id ORDER BY ao.is_primary DESC, (ao.relationship_type='owner') DESC LIMIT 1)
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
		&item.RemediationAdvice, &item.DiscoveredAt, &item.Status, &item.ImportStatusRaw, &item.ImportStatusNormalized,
		&item.ImportStatusMappingReason, &item.SuggestedScopeAssetID, &item.SuggestedAssetID,
		&item.SuggestedAssetValue, &item.SuggestedOrganizationName, &item.MatchedScopeAssetID, &item.MatchedAssetID,
		&item.MatchedAssetValue, &item.MatchedOrganizationName, &item.MatchMethod, &item.MatchConfidence,
		&item.ReviewNote, &item.ReviewedByName, &item.ReviewedAt, &item.ImportedFindingID,
		&item.ImportedFindingCode, &item.AssetCandidateID, &item.CreatedAt, &item.UpdatedAt,
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
	if !principal.CanManageSecurityFindings() && !principal.HasPermission("asset.manage") {
		return FindingImportCandidate{}, ErrForbidden
	}
	if !validUUID(strings.TrimSpace(id)) {
		return FindingImportCandidate{}, invalidField("导入候选标识不正确")
	}
	selectOne := strings.Replace(findingImportCandidateSelect, ", count(*) OVER ()", "", 1)
	item, _, err := scanFindingImportCandidate(s.pool.QueryRow(ctx, selectOne+` WHERE c.id=$1::uuid`, id), false)
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	}
	if err != nil {
		return FindingImportCandidate{}, err
	}
	if !principal.CanManageSecurityFindings() {
		allowed, err := s.canReleaseCandidateFindings(ctx, principal, item.AssetCandidateID)
		if err != nil {
			return FindingImportCandidate{}, err
		}
		if !allowed {
			return FindingImportCandidate{}, ErrForbidden
		}
	}
	return item, nil
}

func (s *Service) ListFindingImportScopeOptions(ctx context.Context, principal auth.Principal, candidateID string) ([]FindingImportScopeOption, error) {
	return s.SearchFindingImportScopeOptions(ctx, principal, candidateID, "")
}

// Search remains bounded for both authorized batch scopes and the global ledger.
// Filtering belongs in SQL, before LIMIT: local filtering cannot reach later rows.
func (s *Service) SearchFindingImportScopeOptions(ctx context.Context, principal auth.Principal, candidateID, keyword string) ([]FindingImportScopeOption, error) {
	if !principal.CanManageSecurityFindings() {
		return nil, ErrForbidden
	}
	candidateID = strings.TrimSpace(candidateID)
	keyword = strings.TrimSpace(keyword)
	if !validUUID(candidateID) {
		return nil, invalidField("导入候选标识不正确")
	}
	var batchID string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(test_batch_id::text,'') FROM security_finding_import_candidates WHERE id=$1::uuid`, candidateID).Scan(&batchID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("load finding import candidate context: %w", err)
	}

	var rows pgx.Rows
	var err error
	if batchID != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT scope.id::text, scope.asset_id::text, scope.asset_type_snapshot, scope.asset_value_snapshot,
			       COALESCE(scope.asset_name_snapshot,''), scope.organization_id::text, scope.organization_code_snapshot,
			       scope.organization_name_snapshot, COALESCE(scope.business_system_id::text,''),
			       COALESCE(scope.business_system_code_snapshot,''), COALESCE(scope.business_system_name_snapshot,''),
			       COALESCE(scope.id=c.suggested_scope_asset_id, false)
			FROM security_finding_import_candidates c
			JOIN security_test_scope_assets scope ON scope.batch_id=c.test_batch_id
			JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
			JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
			WHERE c.id=$1::uuid
 AND ($2='' OR strpos(lower(concat_ws(' ',scope.asset_value_snapshot,scope.asset_name_snapshot,scope.organization_code_snapshot,scope.organization_name_snapshot,scope.business_system_name_snapshot)),lower($2))>0)
			ORDER BY scope.id=c.suggested_scope_asset_id DESC, scope.organization_name_snapshot,
			         scope.asset_type_snapshot, scope.asset_value_snapshot, scope.id LIMIT 200`, candidateID, keyword)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT a.id::text, a.id::text, a.asset_type, a.asset_value,
			       COALESCE(a.asset_name,''), owner.organization_id::text, owner.organization_code,
			       owner.organization_name, COALESCE(system.business_system_id::text,''),
			       COALESCE(system.business_system_code,''), COALESCE(system.business_system_name,''),
			       (a.asset_type=c.asset_type AND (
			          (c.normalized_value <> '' AND a.normalized_value=c.normalized_value)
			          OR (c.normalized_value = '' AND lower(btrim(a.asset_value))=lower(btrim(c.asset_value)))
			       ))
			FROM security_finding_import_candidates c
			JOIN assets a ON a.deleted_at IS NULL
			JOIN LATERAL (
				SELECT ao.organization_id, o.organization_code, o.organization_name
				FROM asset_organizations ao
				JOIN organizations o ON o.id=ao.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
				WHERE ao.asset_id=a.id
				ORDER BY ao.is_primary DESC, (ao.relationship_type='owner') DESC, ao.created_at
				LIMIT 1
			) owner ON true
			LEFT JOIN LATERAL (
				SELECT sa.business_system_id, bs.system_code AS business_system_code, bs.system_name AS business_system_name
				FROM system_assets sa
				JOIN business_systems bs ON bs.id=sa.business_system_id AND bs.deleted_at IS NULL
				WHERE sa.asset_id=a.id
				ORDER BY sa.is_primary DESC, sa.created_at
				LIMIT 1
			) system ON true
			WHERE c.id=$1::uuid
 AND ($2='' OR strpos(lower(concat_ws(' ',a.asset_value,a.asset_name,owner.organization_code,owner.organization_name,system.business_system_name)),lower($2))>0)
			ORDER BY (a.asset_type=c.asset_type AND a.normalized_value=c.normalized_value) DESC,
			         owner.organization_name, a.asset_type, a.asset_value, a.id
			LIMIT 200`, candidateID, keyword)
	}
	if err != nil {
		return nil, fmt.Errorf("list finding import asset options: %w", err)
	}
	defer rows.Close()
	items := make([]FindingImportScopeOption, 0)
	for rows.Next() {
		var item FindingImportScopeOption
		if err := rows.Scan(&item.ID, &item.AssetID, &item.AssetType, &item.AssetValue, &item.AssetName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.BusinessSystemID,
			&item.BusinessSystemCode, &item.BusinessSystemName, &item.Suggested); err != nil {
			return nil, fmt.Errorf("scan finding import asset option: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finding import asset options: %w", err)
	}
	return items, nil
}

// linkFindingToWorkingVersion records a finding against an unpublished
// organization working version. Ownership of a discovered asset is confirmed long
// before the asset register necessarily holds it, and an imported vulnerability
// is real the moment it arrives, so the finding is stored against the working
// version instead of being held back as an untracked import row. Publishing the
// working version fills in the formal asset id.
func findingImportCandidateStatusEvidence(candidate FindingImportCandidate) findingImportStatusEvidence {
	// The persisted normalized value is authoritative. Re-deriving from raw
	// JSON would resurrect risk_accepted/duplicate without their local evidence
	// and could change the meaning of an already reviewed candidate.
	if findingstatus.Valid(candidate.ImportStatusNormalized) {
		return findingImportStatusEvidence{
			Raw: candidate.ImportStatusRaw, Normalized: candidate.ImportStatusNormalized,
			Reason: candidate.ImportStatusMappingReason,
		}
	}
	if candidate.RawData != nil {
		if evidence, err := normalizeFindingImportStatusEvidence(candidate.RawData["status"]); err == nil {
			return evidence
		}
	}
	return findingImportStatusEvidence{Normalized: findingstatus.Pending, Reason: "历史候选缺少状态证据，按兼容规则默认为待处理"}
}

func findingImportCandidateFindingStatus(candidate FindingImportCandidate) string {
	return findingImportCandidateStatusEvidence(candidate).Normalized
}

func (s *Service) linkFindingToWorkingVersion(ctx context.Context, tx pgx.Tx, principal auth.Principal, id string, candidate FindingImportCandidate, input FindingImportCandidateLinkInput, requestID, ipAddress string) (FindingImportCandidate, error) {
	var organizationID string
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text
		FROM inventory_asset_drafts d
		JOIN organizations o ON o.id=d.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
		WHERE d.id=$1::uuid AND d.operation='create' AND d.draft_status IN ('editing','submitted','returned')
		FOR UPDATE OF d`, input.PendingAssetDraftID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("load finding import working version: %w", err)
	}
	if candidate.AssetCandidateID != input.AssetCandidateID {
		return FindingImportCandidate{}, ErrConflict
	}
	var candidateOrganization string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(organization_id::text,'') FROM asset_candidates WHERE id=$1::uuid AND working_version_draft_id=$2::uuid FOR UPDATE`, input.AssetCandidateID, input.PendingAssetDraftID).Scan(&candidateOrganization); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FindingImportCandidate{}, ErrConflict
		}
		return FindingImportCandidate{}, fmt.Errorf("load finding asset candidate: %w", err)
	}
	if candidateOrganization != organizationID {
		return FindingImportCandidate{}, ErrConflict
	}

	var duplicateFindingID string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM security_findings
		WHERE pending_asset_draft_id=$1::uuid AND lower(btrim(title))=lower(btrim($2))
		ORDER BY created_at LIMIT 1`, input.PendingAssetDraftID, candidate.Title).Scan(&duplicateFindingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, fmt.Errorf("check working version finding duplicate: %w", err)
	}
	status := "imported"
	findingID := duplicateFindingID
	statusEvidence := findingImportCandidateStatusEvidence(candidate)
	if errors.Is(err, pgx.ErrNoRows) {
		findingInput := FindingInput{
			Title: candidate.Title, Severity: candidate.Severity, Status: statusEvidence.Normalized,
			AssigneeOrganizationID: organizationID, TestBatchID: candidate.TestBatchID,
			PendingAssetDraftID: input.PendingAssetDraftID,
			VulnerabilityType:   candidate.VulnerabilityType, Description: candidate.Description,
			Evidence: candidate.Evidence, RemediationAdvice: candidate.RemediationAdvice,
			Source: "import", DiscoveredAt: candidate.DiscoveredAt,
			ImportStatusRaw: statusEvidence.Raw, ImportStatusNormalized: statusEvidence.Normalized,
			ImportStatusMappingReason: statusEvidence.Reason,
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
		SET status=$2, matched_scope_asset_id=NULL, matched_asset_id=NULL,
		    review_note=NULLIF($3,''), reviewed_by=$4::uuid, reviewed_at=now(), imported_finding_id=$5::uuid, updated_at=now()
		WHERE id=$1::uuid`, id, status, input.ReviewNote, principal.UserID, findingID); err != nil {
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
	if err := writeAudit(ctx, tx, principal, organizationID, action, "security_finding_import_candidate", id,
		summary, map[string]any{"pending_asset_draft_id": input.PendingAssetDraftID, "finding_id": findingID, "review_note": input.ReviewNote}, requestID, ipAddress); err != nil {
		return FindingImportCandidate{}, err
	}
	return candidate, nil
}

func (s *Service) LinkFindingImportCandidate(ctx context.Context, principal auth.Principal, id string, input FindingImportCandidateLinkInput, requestID, ipAddress string) (FindingImportCandidate, error) {
	if !principal.CanManageSecurityFindings() && !principal.HasPermission("asset.manage") {
		return FindingImportCandidate{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	input.ScopeAssetID = strings.TrimSpace(input.ScopeAssetID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.ReviewNote = strings.TrimSpace(input.ReviewNote)
	input.AssetCandidateID = strings.TrimSpace(input.AssetCandidateID)
	input.PendingAssetDraftID = strings.TrimSpace(input.PendingAssetDraftID)
	if !validUUID(id) {
		return FindingImportCandidate{}, invalidField("待关联漏洞标识不正确")
	}
	if textlength.Count(input.ReviewNote) > 2000 {
		return FindingImportCandidate{}, invalidField("关联备注最多 2000 字")
	}
	targetCount := 0
	for _, value := range []string{input.ScopeAssetID, input.AssetID, input.PendingAssetDraftID} {
		if value != "" {
			targetCount++
		}
	}
	if targetCount == 0 {
		return FindingImportCandidate{}, invalidField("请选择要关联的资产或工作版本")
	}
	if targetCount != 1 {
		return FindingImportCandidate{}, invalidField("关联资产与工作版本只能选择一项")
	}
	for _, field := range []struct{ value, label string }{
		{input.ScopeAssetID, "授权资产"}, {input.AssetID, "关联资产"},
		{input.AssetCandidateID, "资产线索"}, {input.PendingAssetDraftID, "工作版本"},
	} {
		if field.value != "" && !validUUID(field.value) {
			return FindingImportCandidate{}, invalidField(field.label + "标识不正确")
		}
	}
	// Authorization for a working-version release must be based on the
	// candidate stored with the import row, not on an optional client field.
	// Organization operators are allowed to omit asset_candidate_id because the
	// import candidate is the source of truth; deriving it here also prevents a
	// caller from choosing a candidate belonging to another organization.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("begin link finding import candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Confirmation locks the discovery before its import rows. Use the same
	// order here, so a manual release cannot deadlock against confirmation.
	var lockedCandidateID, candidateOrganizationID string
	if err := tx.QueryRow(ctx, `
		SELECT ac.id::text, COALESCE(ac.organization_id::text,'')
		FROM asset_candidates ac
		JOIN security_finding_import_candidates c ON c.asset_candidate_id=ac.id
		WHERE c.id=$1::uuid FOR UPDATE OF ac`, id).Scan(&lockedCandidateID, &candidateOrganizationID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, fmt.Errorf("lock finding asset candidate: %w", err)
	}
	var candidate FindingImportCandidate
	if err := tx.QueryRow(ctx, `
		SELECT finding_import_id::text, COALESCE(test_batch_id::text,''), COALESCE(asset_candidate_id::text,''), title, severity, COALESCE(vulnerability_type,''),
		       description, COALESCE(evidence,''), COALESCE(remediation_advice,''), discovered_at, status,
		       COALESCE(import_status_raw,''), COALESCE(import_status_normalized,''), COALESCE(import_status_mapping_reason,'')
		FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(
		&candidate.FindingImportID, &candidate.TestBatchID, &candidate.AssetCandidateID, &candidate.Title, &candidate.Severity,
		&candidate.VulnerabilityType, &candidate.Description, &candidate.Evidence, &candidate.RemediationAdvice,
		&candidate.DiscoveredAt, &candidate.Status, &candidate.ImportStatusRaw, &candidate.ImportStatusNormalized,
		&candidate.ImportStatusMappingReason); errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("lock finding import candidate: %w", err)
	}
	if candidate.AssetCandidateID != lockedCandidateID {
		return FindingImportCandidate{}, ErrConflict
	}
	if candidate.Status != "pending" {
		return FindingImportCandidate{}, ErrState
	}
	if input.PendingAssetDraftID != "" && input.AssetCandidateID == "" {
		input.AssetCandidateID = candidate.AssetCandidateID
	}
	if input.AssetCandidateID == "" && input.PendingAssetDraftID != "" {
		return FindingImportCandidate{}, invalidField("关联资产工作版本时须指定对应的待确认资产")
	}
	if input.AssetCandidateID != "" && candidate.AssetCandidateID != input.AssetCandidateID {
		return FindingImportCandidate{}, ErrConflict
	}
	if !principal.CanManageSecurityFindings() {
		if candidate.AssetCandidateID == "" {
			return FindingImportCandidate{}, ErrForbidden
		}
		allowed, accessErr := access.CanOperateOrganizationInTx(ctx, tx, principal, candidateOrganizationID, "asset.manage")
		if accessErr != nil {
			return FindingImportCandidate{}, accessErr
		}
		if !allowed {
			return FindingImportCandidate{}, ErrForbidden
		}
	}

	if input.PendingAssetDraftID != "" {
		// The discovery has been confirmed but its working version is not
		// published yet. The finding is recorded against the working version so it
		// is counted and assignable now, and picks up the formal asset id when the
		// organization publishes.
		_, linkErr := s.linkFindingToWorkingVersion(ctx, tx, principal, id, candidate, input, requestID, ipAddress)
		if linkErr != nil {
			return FindingImportCandidate{}, linkErr
		}
		if err := tx.Commit(ctx); err != nil {
			return FindingImportCandidate{}, fmt.Errorf("commit link finding import candidate: %w", err)
		}
		return s.GetFindingImportCandidate(ctx, principal, id)
	}

	selectedID := input.ScopeAssetID
	if candidate.TestBatchID == "" {
		if input.AssetID == "" {
			return FindingImportCandidate{}, invalidField("请选择要关联的正式资产")
		}
		selectedID = input.AssetID
	}
	var scope findingImportScope
	if selectedID != "" && !validUUID(selectedID) {
		// The id comes straight from the request body. A malformed value must be
		// rejected as invalid input, not surfaced as a database cast error that
		// the caller sees as a 500.
		if candidate.TestBatchID != "" {
			return FindingImportCandidate{}, invalidField("测试范围资产标识不正确")
		}
		return FindingImportCandidate{}, invalidField("正式资产标识不正确")
	}
	if candidate.TestBatchID != "" {
		err = tx.QueryRow(ctx, `
			SELECT scope.id::text, scope.asset_id::text, scope.organization_id::text,
			       COALESCE(scope.business_system_id::text,''), scope.asset_type_snapshot,
			       scope.asset_value_snapshot, scope.organization_name_snapshot,
			       COALESCE(scope.business_system_name_snapshot,'')
			FROM security_test_scope_assets scope
			JOIN assets a ON a.id=scope.asset_id AND a.deleted_at IS NULL
			JOIN organizations o ON o.id=scope.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.organization_id=scope.organization_id
			WHERE scope.id=$1::uuid AND scope.batch_id=$2::uuid`, selectedID, candidate.TestBatchID).Scan(
			&scope.ScopeAssetID, &scope.AssetID, &scope.OrganizationID, &scope.BusinessSystemID,
			&scope.AssetTypeSnapshot, &scope.AssetValueSnapshot, &scope.OrganizationName, &scope.BusinessSystemName)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT a.id::text, a.id::text, owner.organization_id::text,
			       COALESCE(system.business_system_id::text,''), a.asset_type, a.asset_value,
			       owner.organization_name, COALESCE(system.business_system_name,'')
			FROM assets a
			JOIN LATERAL (
				SELECT ao.organization_id, o.organization_name
				FROM asset_organizations ao
				JOIN organizations o ON o.id=ao.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
				WHERE ao.asset_id=a.id
				ORDER BY ao.is_primary DESC, (ao.relationship_type='owner') DESC, ao.created_at
				LIMIT 1
			) owner ON true
			LEFT JOIN LATERAL (
				SELECT sa.business_system_id, bs.system_name AS business_system_name
				FROM system_assets sa
				JOIN business_systems bs ON bs.id=sa.business_system_id AND bs.deleted_at IS NULL
				WHERE sa.asset_id=a.id
				ORDER BY sa.is_primary DESC, sa.created_at
				LIMIT 1
			) system ON true
			WHERE a.id=$1::uuid AND a.deleted_at IS NULL`, selectedID).Scan(
			&scope.ScopeAssetID, &scope.AssetID, &scope.OrganizationID, &scope.BusinessSystemID,
			&scope.AssetTypeSnapshot, &scope.AssetValueSnapshot, &scope.OrganizationName, &scope.BusinessSystemName)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, ErrNotFound
	} else if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("load selected finding import asset: %w", err)
	}

	// Permission on the source discovery never grants access to an arbitrary
	// target asset. Validate the resolved target organization before any write.
	if !principal.CanManageSecurityFindings() {
		allowed, err := access.CanOperateOrganizationInTx(ctx, tx, principal, scope.OrganizationID, "asset.manage")
		if err != nil {
			return FindingImportCandidate{}, err
		}
		if !allowed {
			return FindingImportCandidate{}, ErrForbidden
		}
	}

	var duplicateFindingID string
	if candidate.TestBatchID != "" {
		err = tx.QueryRow(ctx, `
			SELECT id::text FROM security_findings
			WHERE test_batch_id=$1::uuid AND asset_id=$2::uuid AND lower(btrim(title))=lower(btrim($3))
			ORDER BY created_at LIMIT 1`, candidate.TestBatchID, scope.AssetID, candidate.Title).Scan(&duplicateFindingID)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT id::text FROM security_findings
			WHERE asset_id=$1::uuid AND lower(btrim(title))=lower(btrim($2))
			ORDER BY created_at LIMIT 1`, scope.AssetID, candidate.Title).Scan(&duplicateFindingID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return FindingImportCandidate{}, fmt.Errorf("check linked finding duplicate: %w", err)
	}
	status := "imported"
	findingID := duplicateFindingID
	statusEvidence := findingImportCandidateStatusEvidence(candidate)
	if errors.Is(err, pgx.ErrNoRows) {
		findingInput := FindingInput{
			Title: candidate.Title, Severity: candidate.Severity, Status: statusEvidence.Normalized,
			AssigneeOrganizationID: scope.OrganizationID, AssetID: scope.AssetID,
			BusinessSystemID: scope.BusinessSystemID, TestBatchID: candidate.TestBatchID,
			VulnerabilityType: candidate.VulnerabilityType, Description: candidate.Description,
			Evidence: candidate.Evidence, RemediationAdvice: candidate.RemediationAdvice,
			Source: "import", DiscoveredAt: candidate.DiscoveredAt,
			ImportStatusRaw: statusEvidence.Raw, ImportStatusNormalized: statusEvidence.Normalized,
			ImportStatusMappingReason: statusEvidence.Reason,
		}
		var duplicate bool
		findingID, duplicate, err = s.createImportedAssetFindingInTx(ctx, tx, principal, findingInput, requestID, ipAddress)
		if err != nil {
			return FindingImportCandidate{}, err
		}
		if duplicate {
			status = "duplicate"
		}
	} else {
		status = "duplicate"
	}
	// matched_scope_asset_id references security_test_scope_assets, so it may only
	// be set when the finding came from a test batch. A batchless import resolves
	// straight to a formal asset and must leave the scope column NULL; writing the
	// asset id there violates the foreign key and rolled the whole link back.
	matchedScopeAssetID := scope.ScopeAssetID
	if candidate.TestBatchID == "" {
		matchedScopeAssetID = ""
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_finding_import_candidates
		SET status=$2, matched_scope_asset_id=NULLIF($3,'')::uuid, matched_asset_id=NULLIF($4,'')::uuid,
		    review_note=NULLIF($5,''), reviewed_by=$6::uuid, reviewed_at=now(), imported_finding_id=$7::uuid, updated_at=now()
		WHERE id=$1::uuid`, id, status, matchedScopeAssetID, scope.AssetID, input.ReviewNote, principal.UserID, findingID); err != nil {
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

// LinkAcceptedAssetCandidateInTx releases all pending import rows behind an
// accepted candidate inside the caller's transaction. It is used by the asset
// candidate confirmation route so draft creation, candidate settlement and
// finding creation either all commit or all roll back.
func (s *Service) LinkAcceptedAssetCandidateInTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, assetCandidateID, pendingAssetDraftID, requestID, ipAddress string) error {
	if !principal.CanManageSecurityFindings() && !principal.HasRole(auth.RoleGroupAdmin) && !principal.HasPermission("asset.manage") {
		return ErrForbidden
	}
	assetCandidateID = strings.TrimSpace(assetCandidateID)
	pendingAssetDraftID = strings.TrimSpace(pendingAssetDraftID)
	if !validUUID(assetCandidateID) {
		return invalidField("待确认资产标识不正确")
	}
	if !validUUID(pendingAssetDraftID) {
		return invalidField("资产工作版本标识不正确")
	}
	var organizationID, draftID string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(organization_id::text,''), COALESCE(working_version_draft_id::text,'')
		FROM asset_candidates WHERE id=$1::uuid FOR UPDATE`, assetCandidateID).Scan(&organizationID, &draftID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock accepted asset candidate for finding link: %w", err)
	}
	if !principal.CanManageSecurityFindings() {
		allowed, err := access.CanOperateOrganizationInTx(ctx, tx, principal, organizationID, "asset.manage")
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
	}
	if draftID != pendingAssetDraftID {
		return ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM security_finding_import_candidates WHERE asset_candidate_id=$1::uuid AND status='pending' ORDER BY source_row_number,created_at FOR UPDATE`, assetCandidateID)
	if err != nil {
		return fmt.Errorf("list pending findings for accepted asset candidate: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate pending findings for accepted asset candidate: %w", err)
	}
	rows.Close()
	for _, id := range ids {
		var candidate FindingImportCandidate
		if err := tx.QueryRow(ctx, `
			SELECT finding_import_id::text, COALESCE(test_batch_id::text,''), COALESCE(asset_candidate_id::text,''), title, severity, COALESCE(vulnerability_type,''),
			       description, COALESCE(evidence,''), COALESCE(remediation_advice,''), discovered_at, status,
			       COALESCE(import_status_raw,''), COALESCE(import_status_normalized,''), COALESCE(import_status_mapping_reason,'')
			FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(
			&candidate.FindingImportID, &candidate.TestBatchID, &candidate.AssetCandidateID, &candidate.Title, &candidate.Severity,
			&candidate.VulnerabilityType, &candidate.Description, &candidate.Evidence, &candidate.RemediationAdvice,
			&candidate.DiscoveredAt, &candidate.Status, &candidate.ImportStatusRaw, &candidate.ImportStatusNormalized,
			&candidate.ImportStatusMappingReason); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock pending finding import candidate: %w", err)
		}
		if candidate.Status != "pending" {
			continue
		}
		if _, err := s.linkFindingToWorkingVersion(ctx, tx, principal, id, candidate, FindingImportCandidateLinkInput{AssetCandidateID: assetCandidateID, PendingAssetDraftID: pendingAssetDraftID}, requestID, ipAddress); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) IgnoreFindingImportCandidate(ctx context.Context, principal auth.Principal, id string, input FindingImportCandidateReviewInput, requestID, ipAddress string) (FindingImportCandidate, error) {
	if !principal.CanManageSecurityFindings() {
		return FindingImportCandidate{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	input.ReviewNote = strings.TrimSpace(input.ReviewNote)
	if !validUUID(id) {
		return FindingImportCandidate{}, invalidField("待关联漏洞标识不正确")
	}
	if input.ReviewNote == "" {
		return FindingImportCandidate{}, invalidField("忽略原因不能为空")
	}
	if textlength.Count(input.ReviewNote) > 2000 {
		return FindingImportCandidate{}, invalidField("忽略原因最多 2000 字")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FindingImportCandidate{}, fmt.Errorf("begin ignore finding import candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var importID, batchID, status string
	if err := tx.QueryRow(ctx, `SELECT finding_import_id::text,COALESCE(test_batch_id::text,''),status FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(&importID, &batchID, &status); errors.Is(err, pgx.ErrNoRows) {
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

// FindingImportLinkResult makes partial linking explicit so failed rows remain
// pending and can be retried without re-importing the file.
type FindingImportLinkResult struct {
	Total   int `json:"total"`
	Linked  int `json:"linked"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

// LinkAcceptedAssetCandidate links all pending findings for an accepted asset.
// It is idempotent and retains failed candidates as pending.
func (s *Service) LinkAcceptedAssetCandidate(ctx context.Context, principal auth.Principal, assetCandidateID, assetID, requestID, ipAddress string) error {
	_, err := s.RetryAcceptedAssetCandidate(ctx, principal, assetCandidateID, assetID, requestID, ipAddress)
	return err
}

func (s *Service) RetryAcceptedAssetCandidate(ctx context.Context, principal auth.Principal, assetCandidateID, assetID, requestID, ipAddress string) (FindingImportLinkResult, error) {
	result := FindingImportLinkResult{}
	assetCandidateID, assetID = strings.TrimSpace(assetCandidateID), strings.TrimSpace(assetID)
	if !validUUID(assetCandidateID) {
		return result, invalidField("待确认资产标识不正确")
	}
	// Headquarters releases findings with security_finding.manage. A subsidiary
	// that has been delegated the confirmation of its own asset candidates must be
	// able to release the findings that came with them, otherwise it confirms the
	// asset and the vulnerabilities behind it stay stuck in the import queue.
	if !principal.CanManageSecurityFindings() {
		organizationID, candidateErr := s.candidateOrganization(ctx, assetCandidateID)
		if candidateErr != nil {
			return result, candidateErr
		}
		allowed, accessErr := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
		if accessErr != nil {
			return result, accessErr
		}
		if !allowed {
			return result, ErrForbidden
		}
	}
	// A confirmed discovery has an unpublished working version rather than a
	// formal asset until the organization publishes it. Findings released now are
	// attached to that working version and move onto the formal asset when it is
	// published, so an imported vulnerability is never left untracked just
	// because the asset register is behind.
	var draftID string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(matched_asset_id::text,''),COALESCE(working_version_draft_id::text,'') FROM asset_candidates WHERE id=$1::uuid`, assetCandidateID).Scan(&assetID, &draftID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrNotFound
		}
		return result, fmt.Errorf("load asset candidate for finding link: %w", err)
	}
	if draftID == "" && !validUUID(assetID) {
		return result, invalidField("待确认资产尚未关联有效的正式资产或工作版本")
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM security_finding_import_candidates WHERE asset_candidate_id=$1::uuid AND status='pending' ORDER BY source_row_number,created_at`, assetCandidateID)
	if err != nil {
		return result, fmt.Errorf("list findings for accepted asset candidate: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.Total = len(ids)
	var firstErr error
	for _, id := range ids {
		if _, err := s.LinkFindingImportCandidate(ctx, principal, id, FindingImportCandidateLinkInput{AssetID: assetID, AssetCandidateID: assetCandidateID, PendingAssetDraftID: draftID}, requestID, ipAddress); err != nil {
			result.Failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("link finding import candidate %s: %w", id, err)
			}
			continue
		}
		result.Linked++
	}
	result.Pending = result.Failed
	if firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

// canReleaseCandidateFindings reports whether a non-headquarters user may link
// findings that belong to the given asset candidate. Without an asset candidate
// the answer is no: headquarters keeps every finding that is not attached to an
// organization asset it has been given.
func (s *Service) canReleaseCandidateFindings(ctx context.Context, principal auth.Principal, assetCandidateID string) (bool, error) {
	assetCandidateID = strings.TrimSpace(assetCandidateID)
	if assetCandidateID == "" || !principal.HasPermission("asset.manage") {
		return false, nil
	}
	organizationID, err := s.candidateOrganization(ctx, assetCandidateID)
	if err != nil {
		return false, err
	}
	if organizationID == "" {
		return false, nil
	}
	return access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
}

// candidateOrganization returns the organization that owns an asset candidate,
// or an empty string when ownership has not been resolved yet.
func (s *Service) candidateOrganization(ctx context.Context, assetCandidateID string) (string, error) {
	var organizationID string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(organization_id::text,'') FROM asset_candidates WHERE id=$1::uuid`, assetCandidateID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load asset candidate organization: %w", err)
	}
	return organizationID, nil
}

// createImportedAssetFindingInTx tolerates a duplicate that wins after the
// advisory pre-check. PostgreSQL aborts a transaction on a unique violation:
// the insert must use a savepoint before the surviving finding can be read.
func (s *Service) createImportedAssetFindingInTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, input FindingInput, requestID, ipAddress string) (string, bool, error) {
	attempt, err := tx.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin imported finding savepoint: %w", err)
	}
	defer attempt.Rollback(ctx)
	findingID, _, err := s.createFindingInTx(ctx, attempt, principal, input, requestID, ipAddress)
	if err == nil {
		if err := attempt.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("commit imported finding savepoint: %w", err)
		}
		return findingID, false, nil
	}
	if rollbackErr := attempt.Rollback(ctx); rollbackErr != nil {
		return "", false, fmt.Errorf("rollback imported finding savepoint: %w", rollbackErr)
	}
	if !isFindingDuplicateViolation(err) {
		return "", false, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM security_findings
		WHERE test_batch_id IS NOT DISTINCT FROM NULLIF($1,'')::uuid
		  AND asset_id=$2::uuid AND lower(btrim(title))=lower(btrim($3))
		ORDER BY created_at LIMIT 1`, input.TestBatchID, input.AssetID, input.Title).Scan(&findingID); err != nil {
		return "", false, fmt.Errorf("resolve concurrently created finding: %w", err)
	}
	return findingID, true, nil
}

// isFindingDuplicateViolation reports whether err is the unique violation raised
// by security_findings_batch_asset_title_unique, which is the signal that a
// parallel import already recorded this issue for the same asset.
func isFindingDuplicateViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "security_findings_batch_asset_title_unique"
}
