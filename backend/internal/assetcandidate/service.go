package assetcandidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
)

type Candidate struct {
	ID                    string         `json:"id"`
	AssetType             string         `json:"asset_type"`
	AssetValue            string         `json:"asset_value"`
	NormalizedValue       string         `json:"normalized_value"`
	Name                  string         `json:"name,omitempty"`
	OrganizationID        string         `json:"organization_id"`
	OrganizationCode      string         `json:"organization_code"`
	OrganizationName      string         `json:"organization_name"`
	Source                string         `json:"source"`
	SourceReference       string         `json:"source_reference,omitempty"`
	SuggestedData         map[string]any `json:"suggested_data"`
	Status                string         `json:"status"` // 审核状态，不代表正式资产是否启用
	ReviewStatus          string         `json:"review_status"`
	MatchedAssetID        string         `json:"matched_asset_id,omitempty"`
	WorkingVersionDraftID string         `json:"working_version_draft_id,omitempty"`
	ReviewNote            string         `json:"review_note,omitempty"`
	CreatedBy             string         `json:"created_by,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	ReviewedBy            string         `json:"reviewed_by,omitempty"`
	ReviewedAt            *time.Time     `json:"reviewed_at,omitempty"`
	FindingCount          int            `json:"finding_count,omitempty"`
	HighestSeverity       string         `json:"highest_severity,omitempty"`
}

type CreateInput struct {
	AssetType       string         `json:"asset_type"`
	AssetValue      string         `json:"asset_value"`
	Name            string         `json:"name"`
	OrganizationID  string         `json:"organization_id"`
	Source          string         `json:"source"`
	SourceReference string         `json:"source_reference"`
	SuggestedData   map[string]any `json:"suggested_data"`
	Notes           string         `json:"notes"`
}

type AcceptInput struct {
	asset.CreateInput
	ReviewNote string `json:"review_note"`
}

type ReviewInput struct {
	ReviewNote string `json:"review_note"`
}
type DuplicateInput struct {
	MatchedAssetID string `json:"matched_asset_id"`
	ReviewNote     string `json:"review_note"`
}

type Service struct {
	pool   *pgxpool.Pool
	assets *asset.Service
}

func NewService(pool *pgxpool.Pool, assets *asset.Service) *Service {
	return &Service{pool: pool, assets: assets}
}

func requireGroupAdmin(principal auth.Principal) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	return nil
}

// requireCandidateReader allows headquarters, and any organization user that
// holds asset.manage somewhere, to open the candidate queue. Row-level scope is
// applied by the query itself rather than here.
func requireCandidateReader(principal auth.Principal) error {
	if principal.HasRole("group_admin") || principal.HasPermission("asset.manage") {
		return nil
	}
	return ErrForbidden
}

func (s *Service) List(ctx context.Context, principal auth.Principal, status string) ([]Candidate, error) {
	if err := requireCandidateReader(principal); err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status != "" && !validStatus(status) {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, candidateScopeCTE+`
        SELECT c.id::text, c.asset_type, c.asset_value, c.normalized_value,
               COALESCE(c.suggested_data->>'name', ''), COALESCE(o.id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
               c.source, COALESCE(c.source_reference, ''), c.suggested_data, c.status,
               COALESCE(c.matched_asset_id::text, ''), COALESCE(c.working_version_draft_id::text, ''),
               COALESCE(c.review_note, ''), COALESCE(c.created_by::text, ''),
               c.created_at, c.updated_at, COALESCE(c.reviewed_by::text, ''), c.reviewed_at,
               (SELECT count(*) FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),
               COALESCE((SELECT CASE max(CASE fic.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END) WHEN 4 THEN 'critical' WHEN 3 THEN 'high' WHEN 2 THEN 'medium' WHEN 1 THEN 'low' ELSE '' END FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),'')
        FROM asset_candidates c LEFT JOIN organizations o ON o.id=c.organization_id
        WHERE ($3='' OR c.status=$3) AND ($2::boolean OR c.organization_id IN (SELECT id FROM accessible))
        ORDER BY CASE WHEN c.status='pending' THEN 0 ELSE 1 END, c.created_at DESC`, principal.UserID, principal.HasRole("group_admin"), status)
	if err != nil {
		return nil, fmt.Errorf("list asset candidates: %w", err)
	}
	defer rows.Close()
	items := make([]Candidate, 0)
	for rows.Next() {
		var i Candidate
		var raw []byte
		if err := rows.Scan(&i.ID, &i.AssetType, &i.AssetValue, &i.NormalizedValue, &i.Name, &i.OrganizationID, &i.OrganizationCode, &i.OrganizationName, &i.Source, &i.SourceReference, &raw, &i.Status, &i.MatchedAssetID, &i.WorkingVersionDraftID, &i.ReviewNote, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.ReviewedBy, &i.ReviewedAt, &i.FindingCount, &i.HighestSeverity); err != nil {
			return nil, fmt.Errorf("scan asset candidate: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &i.SuggestedData)
		}
		if i.SuggestedData == nil {
			i.SuggestedData = map[string]any{}
		}
		i.ReviewStatus = i.Status
		items = append(items, i)
	}
	return items, rows.Err()
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, status string, query pagination.Query) (pagination.Result[Candidate], error) {
	if err := requireCandidateReader(principal); err != nil {
		return pagination.Result[Candidate]{}, err
	}
	status = strings.TrimSpace(status)
	if status != "" && !validStatus(status) {
		return pagination.Result[Candidate]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, candidateScopeCTE+`
        SELECT c.id::text, c.asset_type, c.asset_value, c.normalized_value,
               COALESCE(c.suggested_data->>'name', ''), COALESCE(o.id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
               c.source, COALESCE(c.source_reference, ''), c.suggested_data, c.status,
               COALESCE(c.matched_asset_id::text, ''), COALESCE(c.working_version_draft_id::text, ''),
               COALESCE(c.review_note, ''), COALESCE(c.created_by::text, ''),
               c.created_at, c.updated_at, COALESCE(c.reviewed_by::text, ''), c.reviewed_at,
               (SELECT count(*) FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),
               COALESCE((SELECT CASE max(CASE fic.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END) WHEN 4 THEN 'critical' WHEN 3 THEN 'high' WHEN 2 THEN 'medium' WHEN 1 THEN 'low' ELSE '' END FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),''), count(*) OVER ()
        FROM asset_candidates c LEFT JOIN organizations o ON o.id=c.organization_id
        WHERE ($3='' OR c.status=$3) AND ($2::boolean OR c.organization_id IN (SELECT id FROM accessible))
        ORDER BY CASE WHEN c.status='pending' THEN 0 ELSE 1 END, c.created_at DESC
        LIMIT $4 OFFSET $5`, principal.UserID, principal.HasRole("group_admin"), status, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Candidate]{}, fmt.Errorf("search asset candidates: %w", err)
	}
	defer rows.Close()
	items := make([]Candidate, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var i Candidate
		var raw []byte
		if err := rows.Scan(&i.ID, &i.AssetType, &i.AssetValue, &i.NormalizedValue, &i.Name, &i.OrganizationID, &i.OrganizationCode, &i.OrganizationName, &i.Source, &i.SourceReference, &raw, &i.Status, &i.MatchedAssetID, &i.WorkingVersionDraftID, &i.ReviewNote, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.ReviewedBy, &i.ReviewedAt, &i.FindingCount, &i.HighestSeverity, &total); err != nil {
			return pagination.Result[Candidate]{}, fmt.Errorf("scan asset candidate page: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &i.SuggestedData)
		}
		if i.SuggestedData == nil {
			i.SuggestedData = map[string]any{}
		}
		i.ReviewStatus = i.Status
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Candidate]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, candidateScopeCTE+`SELECT count(*) FROM asset_candidates c WHERE ($3='' OR c.status=$3) AND ($2::boolean OR c.organization_id IN (SELECT id FROM accessible))`, principal.UserID, principal.HasRole("group_admin"), status).Scan(&total); err != nil {
			return pagination.Result[Candidate]{}, fmt.Errorf("count asset candidates: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

// Get loads a candidate the principal may read. Organization users see the
// candidates of the organizations they are appointed to, because a subsidiary
// needs to know what a scan found in its estate even when headquarters still
// owns the confirmation.
func (s *Service) Get(ctx context.Context, principal auth.Principal, id string) (Candidate, error) {
	if err := requireCandidateReader(principal); err != nil {
		return Candidate{}, err
	}
	item, err := s.listByID(ctx, id)
	if err != nil {
		return Candidate{}, err
	}
	viewable, err := s.canViewCandidate(ctx, principal, item.OrganizationID)
	if err != nil {
		return Candidate{}, err
	}
	if !viewable {
		return Candidate{}, ErrForbidden
	}
	return item, nil
}

func (s *Service) listByID(ctx context.Context, id string) (Candidate, error) {
	var i Candidate
	var raw []byte
	err := s.pool.QueryRow(ctx, `
        SELECT c.id::text, c.asset_type, c.asset_value, c.normalized_value,
               COALESCE(c.suggested_data->>'name', ''), COALESCE(o.id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
               c.source, COALESCE(c.source_reference, ''), c.suggested_data, c.status,
               COALESCE(c.matched_asset_id::text, ''), COALESCE(c.working_version_draft_id::text, ''),
               COALESCE(c.review_note, ''), COALESCE(c.created_by::text, ''),
               c.created_at, c.updated_at, COALESCE(c.reviewed_by::text, ''), c.reviewed_at,
               (SELECT count(*) FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),
               COALESCE((SELECT CASE max(CASE fic.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END) WHEN 4 THEN 'critical' WHEN 3 THEN 'high' WHEN 2 THEN 'medium' WHEN 1 THEN 'low' ELSE '' END FROM security_finding_import_candidates fic WHERE fic.asset_candidate_id=c.id AND fic.status='pending'),'')
        FROM asset_candidates c LEFT JOIN organizations o ON o.id=c.organization_id WHERE c.id=$1::uuid`, id).
		Scan(&i.ID, &i.AssetType, &i.AssetValue, &i.NormalizedValue, &i.Name, &i.OrganizationID, &i.OrganizationCode, &i.OrganizationName, &i.Source, &i.SourceReference, &raw, &i.Status, &i.MatchedAssetID, &i.WorkingVersionDraftID, &i.ReviewNote, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.ReviewedBy, &i.ReviewedAt, &i.FindingCount, &i.HighestSeverity)
	if errors.Is(err, pgx.ErrNoRows) {
		return Candidate{}, ErrNotFound
	}
	if err != nil {
		return Candidate{}, fmt.Errorf("get asset candidate: %w", err)
	}
	_ = json.Unmarshal(raw, &i.SuggestedData)
	if i.SuggestedData == nil {
		i.SuggestedData = map[string]any{}
	}
	i.ReviewStatus = i.Status
	return i, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, in CreateInput, requestID, ip string) (Candidate, error) {
	if err := requireGroupAdmin(principal); err != nil {
		return Candidate{}, err
	}
	in.AssetType = strings.ToLower(strings.TrimSpace(in.AssetType))
	in.AssetValue = strings.TrimSpace(in.AssetValue)
	in.OrganizationID = strings.TrimSpace(in.OrganizationID)
	in.Name = strings.TrimSpace(in.Name)
	in.Source = strings.TrimSpace(in.Source)
	in.SourceReference = strings.TrimSpace(in.SourceReference)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Source != "manual" && in.Source != "api" && in.Source != "import" && in.Source != "finding_import" {
		return Candidate{}, ErrInvalid
	}
	normalized, err := asset.NormalizeValue(in.AssetType, in.AssetValue)
	if err != nil || (in.Source != "finding_import" && in.OrganizationID == "") || len(in.Name) > 255 || len(in.SourceReference) > 255 || len(in.Notes) > 5000 {
		return Candidate{}, ErrInvalid
	}
	suggested := in.SuggestedData
	if suggested == nil {
		suggested = map[string]any{}
	}
	if in.Name != "" {
		suggested["name"] = in.Name
	}
	if in.Notes != "" {
		suggested["notes"] = in.Notes
	}
	data, err := json.Marshal(suggested)
	if err != nil {
		return Candidate{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Candidate{}, fmt.Errorf("begin candidate: %w", err)
	}
	defer tx.Rollback(ctx)
	if in.OrganizationID != "" {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT status='enabled' FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL`, in.OrganizationID).Scan(&enabled); errors.Is(err, pgx.ErrNoRows) {
			return Candidate{}, ErrNotFound
		} else if err != nil {
			return Candidate{}, err
		}
		if !enabled {
			return Candidate{}, ErrInvalid
		}
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO asset_candidates(asset_type,asset_value,normalized_value,suggested_data,organization_id,source,source_reference,status,created_by) VALUES($1,$2,$3,$4::jsonb,NULLIF($5,'')::uuid,$6,NULLIF($7,''),'pending',$8::uuid) RETURNING id::text`, in.AssetType, in.AssetValue, normalized, data, in.OrganizationID, in.Source, in.SourceReference, principal.UserID).Scan(&id)
	if err != nil {
		return Candidate{}, fmt.Errorf("insert asset candidate: %w", err)
	}
	if err = s.audit(ctx, tx, principal, in.OrganizationID, "asset_candidate.create", id, "新增待确认资产", map[string]any{"asset_type": in.AssetType, "asset_value": in.AssetValue, "source": in.Source}, requestID, ip); err != nil {
		return Candidate{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Candidate{}, fmt.Errorf("commit candidate: %w", err)
	}
	return s.listByID(ctx, id)
}

// Accept confirms ownership of a discovered asset. It records an organization
// working version rather than a formal asset: the finding import knows an
// address exists, not who owns it or how it should be classified, and writing
// assets directly skipped the working version, the reporting task, the snapshot
// and the quality checks that every other asset change goes through.
//
// The candidate stays pending and carries the draft id until the working version
// is published, at which point inventorytask settles it and releases the
// findings that were waiting on the asset.
func (s *Service) Accept(ctx context.Context, principal auth.Principal, id string, in AcceptInput, requestID, ip string) (asset.Asset, error) {
	candidate, err := s.confirmCandidate(ctx, principal, id)
	if err != nil {
		return asset.Asset{}, err
	}
	in.Type = candidate.AssetType
	in.Value = candidate.AssetValue
	if candidate.OrganizationID != "" {
		in.OrganizationID = candidate.OrganizationID
	}
	if strings.TrimSpace(in.OrganizationID) == "" {
		return asset.Asset{}, ErrInvalid
	}
	if in.Name == "" {
		in.Name = candidate.Name
	}
	if in.Notes == "" {
		if v, ok := candidate.SuggestedData["notes"].(string); ok {
			in.Notes = v
		}
	}
	source := "manual"
	if candidate.Source == "finding_import" {
		source = "finding_import"
	}
	if source == "finding_import" {
		// The scan recorded an endpoint; the port is evidence about the host, not
		// part of its identity. Normalising has already collapsed host:port onto
		// the address, so the port is kept as a note for the owner to classify.
		if host, port, ok := asset.SplitHostPort(in.Value); ok {
			if in.Notes == "" {
				in.Notes = "漏洞导入记录端口 " + port
			} else {
				in.Notes = in.Notes + "；漏洞导入记录端口 " + port
			}
			in.Value = host
		}
	}
	item, err := s.assets.CreateWorkingVersion(ctx, principal, in.CreateInput, source, requestID, ip, func(ctxTx pgx.Tx, created asset.Asset) error {
		var current, org, draftID string
		if err := ctxTx.QueryRow(ctx, `SELECT status,COALESCE(organization_id::text,''),COALESCE(working_version_draft_id::text,'') FROM asset_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(&current, &org, &draftID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock asset candidate for acceptance: %w", err)
		}
		if current != "pending" {
			return ErrConflict
		}
		if draftID != "" {
			// A working version already exists for this candidate; accepting again
			// must not leave a second unpublished draft behind for the same value.
			return ErrConflict
		}
		// Finding imports may arrive before ownership is known. Once an administrator
		// accepts the discovery, persist the selected organization on the candidate as
		// well so subsequent review/list responses retain the resolved ownership.
		resolvedOrganizationID := org
		if resolvedOrganizationID == "" {
			resolvedOrganizationID = in.OrganizationID
		}
		if _, err := ctxTx.Exec(ctx, `UPDATE asset_candidates SET organization_id=$2::uuid,working_version_draft_id=$3::uuid,review_note=NULLIF($4,''),reviewed_by=$5::uuid,reviewed_at=now(),updated_at=now() WHERE id=$1::uuid`, id, resolvedOrganizationID, created.ID, strings.TrimSpace(in.ReviewNote), principal.UserID); err != nil {
			return fmt.Errorf("accept asset candidate: %w", err)
		}
		// The findings behind this discovery no longer wait on asset bookkeeping:
		// they are attached to the working version and move to the formal asset
		// when it is published.
		if _, err := ctxTx.Exec(ctx, `
			UPDATE security_finding_import_candidates
			SET matched_asset_id=NULL, updated_at=now()
			WHERE asset_candidate_id=$1::uuid AND status='pending'`, id); err != nil {
			return fmt.Errorf("detach pending findings from asset candidate: %w", err)
		}
		return s.audit(ctx, ctxTx, principal, resolvedOrganizationID, "asset_candidate.accept", id, "确认待确认资产归属并生成未上报工作版本", map[string]any{"working_version_draft_id": created.ID, "review_note": strings.TrimSpace(in.ReviewNote)}, requestID, ip)
	})
	if err != nil {
		return asset.Asset{}, mapAssetError(err)
	}
	return item, nil
}
func (s *Service) Ignore(ctx context.Context, p auth.Principal, id string, in ReviewInput, rid, ip string) error {
	return s.review(ctx, p, id, "ignored", "", in.ReviewNote, rid, ip)
}
func (s *Service) Invalidate(ctx context.Context, p auth.Principal, id string, in ReviewInput, rid, ip string) error {
	return s.review(ctx, p, id, "invalid", "", in.ReviewNote, rid, ip)
}
func (s *Service) MarkDuplicate(ctx context.Context, p auth.Principal, id string, in DuplicateInput, rid, ip string) error {
	if strings.TrimSpace(in.MatchedAssetID) == "" {
		return ErrInvalid
	}
	return s.review(ctx, p, id, "duplicate", in.MatchedAssetID, in.ReviewNote, rid, ip)
}
func (s *Service) review(ctx context.Context, p auth.Principal, id, status, matched, note, rid, ip string) error {
	if _, err := s.confirmCandidate(ctx, p, id); err != nil {
		return err
	}
	if !validStatus(status) || status == "pending" {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin candidate review: %w", err)
	}
	defer tx.Rollback(ctx)
	var current, org string
	err = tx.QueryRow(ctx, `SELECT status,organization_id::text FROM asset_candidates WHERE id=$1::uuid FOR UPDATE`, id).Scan(&current, &org)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != "pending" {
		return ErrConflict
	}
	if status == "duplicate" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1::uuid AND deleted_at IS NULL)`, matched).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	_, err = tx.Exec(ctx, `UPDATE asset_candidates SET status=$2,matched_asset_id=NULLIF($3,'')::uuid,review_note=NULLIF($4,''),reviewed_by=$5::uuid,reviewed_at=now(),updated_at=now() WHERE id=$1::uuid`, id, status, matched, strings.TrimSpace(note), p.UserID)
	if err != nil {
		return fmt.Errorf("update candidate review: %w", err)
	}
	action := "asset_candidate." + map[string]string{"accepted": "accept", "ignored": "ignore", "duplicate": "mark_duplicate", "invalid": "invalidate"}[status]
	if err = s.audit(ctx, tx, p, org, action, id, "处理待确认资产", map[string]any{"status": status, "matched_asset_id": matched, "review_note": strings.TrimSpace(note)}, rid, ip); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit candidate review: %w", err)
	}
	return nil
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, p auth.Principal, org, action, id, summary string, details map[string]any, rid, ip string) error {
	data, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,NULLIF($2,'')::uuid,$3,'asset_candidate',$4::uuid,$5,$6::jsonb,NULLIF($7,'')::inet,NULLIF($8,''))`, p.UserID, org, action, id, summary, data, ip, rid)
	if err != nil {
		return fmt.Errorf("write candidate audit: %w", err)
	}
	return nil
}
func validStatus(s string) bool {
	switch s {
	case "pending", "accepted", "ignored", "duplicate", "invalid":
		return true
	}
	return false
}
func mapAssetError(err error) error {
	switch {
	case errors.Is(err, asset.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, asset.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, asset.ErrConflict):
		return ErrConflict
	case errors.Is(err, asset.ErrInvalid):
		return ErrInvalid
	}
	return err
}
