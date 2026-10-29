package assetcandidate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
)

// ErrSelfServiceClosed is returned when an organization user tries to confirm a
// candidate while headquarters has not delegated that right to the organization.
// It is a policy answer rather than a permission answer: the user may well hold
// asset.manage, the organization is simply not allowed to settle asset ownership
// on its own yet.
var ErrSelfServiceClosed = errors.New("asset candidate self service is closed for this organization")

// candidateScopeCTE mirrors the organization scope used by the asset register:
// group administrators and the group security candidate-reader see every candidate,
// organization users see the candidates of the organizations they can operate.
const candidateScopeCTE = `
WITH RECURSIVE descendant_roots(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
    JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
    JOIN organizations root ON root.id = ur.organization_id AND root.status = 'enabled' AND root.deleted_at IS NULL
    WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
), descendants(id) AS (
    SELECT id FROM descendant_roots
    UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
    WHERE child.status = 'enabled' AND child.deleted_at IS NULL
), accessible(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
    JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
    JOIN organizations direct ON direct.id = ur.organization_id AND direct.status = 'enabled' AND direct.deleted_at IS NULL
    WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
      AND ur.data_scope IN ('organization', 'organization_and_descendants')
    UNION SELECT id FROM descendants
)`

// SelfServicePolicy records the delegation headquarters granted to one
// organization: whether it may confirm its own candidates, and for which asset
// types. An organization without a row, or with allow_self_service false, keeps
// its candidates in the headquarters review queue.
type SelfServicePolicy struct {
	OrganizationID             string   `json:"organization_id"`
	OrganizationName           string   `json:"organization_name"`
	AllowSelfService           bool     `json:"allow_self_service"`
	AllowedAssetTypes          []string `json:"allowed_asset_types"`
	RequireEnabledOrganization bool     `json:"require_enabled_organization"`
	UpdatedAt                  string   `json:"updated_at,omitempty"`
}

type SelfServicePolicyInput struct {
	OrganizationID             string   `json:"organization_id"`
	AllowSelfService           bool     `json:"allow_self_service"`
	AllowedAssetTypes          []string `json:"allowed_asset_types"`
	RequireEnabledOrganization bool     `json:"require_enabled_organization"`
}

func validPolicyAssetType(t string) bool {
	switch t {
	case "ip", "domain", "url", "hostname":
		return true
	}
	return false
}

func normalizePolicyTypes(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if !validPolicyAssetType(t) {
			return nil, invalidInput("自助确认策略的资产类型仅支持 IP、域名、URL 和主机名")
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}

// LoadSelfServicePolicies returns the delegation for every organization the
// principal may see, so the review page can show who may confirm their own
// discoveries and who still waits for headquarters.
func (s *Service) LoadSelfServicePolicies(ctx context.Context, principal auth.Principal) ([]SelfServicePolicy, error) {
	if !principal.HasRole("group_admin") && !principal.HasPermission("asset.manage") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, candidateScopeCTE+`
		SELECT o.id::text, o.organization_name,
		       COALESCE(p.allow_self_service, false),
		       COALESCE(p.allowed_asset_types, '{}'),
		       COALESCE(p.require_enabled_organization, true),
		       COALESCE(to_char(p.updated_at, 'YYYY-MM-DD HH24:MI'), '')
		FROM organizations o
		LEFT JOIN asset_candidate_self_service_policies p ON p.organization_id = o.id
		WHERE o.status = 'enabled' AND o.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY o.organization_code`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list asset candidate self service policies: %w", err)
	}
	defer rows.Close()
	out := make([]SelfServicePolicy, 0)
	for rows.Next() {
		var p SelfServicePolicy
		if err := rows.Scan(&p.OrganizationID, &p.OrganizationName, &p.AllowSelfService, &p.AllowedAssetTypes, &p.RequireEnabledOrganization, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.AllowedAssetTypes == nil {
			p.AllowedAssetTypes = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetSelfServicePolicy records or replaces the delegation. Headquarters owns
// this decision, so only group administrators may change it.
func (s *Service) SetSelfServicePolicy(ctx context.Context, principal auth.Principal, in SelfServicePolicyInput) (SelfServicePolicy, error) {
	if !principal.HasRole("group_admin") {
		return SelfServicePolicy{}, ErrForbidden
	}
	in.OrganizationID = strings.TrimSpace(in.OrganizationID)
	if in.OrganizationID == "" {
		return SelfServicePolicy{}, invalidInput("请选择自助确认策略所属组织")
	}
	if !candidateUUIDPattern.MatchString(in.OrganizationID) {
		return SelfServicePolicy{}, invalidInput("自助确认策略的组织标识格式不正确")
	}
	types, err := normalizePolicyTypes(in.AllowedAssetTypes)
	if err != nil {
		return SelfServicePolicy{}, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL)`, in.OrganizationID).Scan(&exists); err != nil {
		return SelfServicePolicy{}, err
	}
	if !exists {
		return SelfServicePolicy{}, ErrNotFound
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO asset_candidate_self_service_policies(organization_id,allow_self_service,allowed_asset_types,require_enabled_organization,updated_by)
		VALUES($1::uuid,$2,$3,$4,$5::uuid)
		ON CONFLICT (organization_id) DO UPDATE
		SET allow_self_service=EXCLUDED.allow_self_service,
		    allowed_asset_types=EXCLUDED.allowed_asset_types,
		    require_enabled_organization=EXCLUDED.require_enabled_organization,
		    updated_at=now(), updated_by=EXCLUDED.updated_by
		RETURNING to_char(updated_at, 'YYYY-MM-DD HH24:MI')`, in.OrganizationID, in.AllowSelfService, types, in.RequireEnabledOrganization, principal.UserID).Scan(new(string))
	if err != nil {
		return SelfServicePolicy{}, fmt.Errorf("save asset candidate self service policy: %w", err)
	}
	policies, err := s.LoadSelfServicePolicies(ctx, principal)
	if err != nil {
		return SelfServicePolicy{}, err
	}
	for _, p := range policies {
		if p.OrganizationID == in.OrganizationID {
			return p, nil
		}
	}
	return SelfServicePolicy{}, ErrNotFound
}

// canViewCandidate reports whether the principal may read a candidate. Reading is
// wider than accepting: an organization needs to see its own discoveries even
// while headquarters retains the decision.
func (s *Service) canViewCandidate(ctx context.Context, principal auth.Principal, organizationID string) (bool, error) {
	if principal.CanViewAssetCandidates() {
		return true, nil
	}
	if !principal.HasPermission("asset.manage") {
		return false, nil
	}
	return access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
}

// canConfirmCandidate reports whether the principal may turn this candidate into
// an organization working version. Group administrators always may. Organization
// users need asset.manage in the owning organization and an open delegation that
// covers the candidate's asset type; otherwise the candidate stays with
// headquarters instead of failing with a generic permission error.
func (s *Service) canConfirmCandidate(ctx context.Context, principal auth.Principal, candidate Candidate, organizationID string) (bool, error) {
	if principal.HasRole("group_admin") {
		return true, nil
	}
	// The group security operator imports findings and owns the resulting
	// discoveries, but must not be able to edit the formal asset ledger. Ownership
	// of a discovery is therefore a dedicated capability: without it the import
	// queue dead-ends and only a group administrator could ever settle it.
	if principal.HasPermission("asset_candidate.confirm") {
		return true, nil
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil || !allowed {
		return false, err
	}
	var allow bool
	var types []string
	var requireEnabled bool
	err = s.pool.QueryRow(ctx, `
		SELECT p.allow_self_service, p.allowed_asset_types, p.require_enabled_organization
		FROM asset_candidate_self_service_policies p
		JOIN organizations o ON o.id = p.organization_id
		WHERE p.organization_id = $1::uuid`, organizationID).Scan(&allow, &types, &requireEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load asset candidate self service policy: %w", err)
	}
	if !allow {
		return false, nil
	}
	if requireEnabled {
		var enabled bool
		if err := s.pool.QueryRow(ctx, `SELECT status='enabled' FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL`, organizationID).Scan(&enabled); err != nil {
			return false, err
		}
		if !enabled {
			return false, nil
		}
	}
	if len(types) == 0 {
		// An empty allow-list means the delegation covers every asset type.
		return true, nil
	}
	for _, t := range types {
		if t == candidate.AssetType {
			return true, nil
		}
	}
	return false, nil
}

// confirmCandidate loads a candidate and applies the confirmation policy, so the
// error the caller sees distinguishes "not yours" from "still needs headquarters".
func (s *Service) confirmCandidate(ctx context.Context, principal auth.Principal, id string) (Candidate, error) {
	candidate, err := s.Get(ctx, principal, id)
	if err != nil {
		return Candidate{}, err
	}
	if candidate.Status != "pending" {
		return Candidate{}, ErrConflict
	}
	organizationID := strings.TrimSpace(candidate.OrganizationID)
	allowed, err := s.canConfirmCandidate(ctx, principal, candidate, organizationID)
	if err != nil {
		return Candidate{}, err
	}
	if !allowed {
		if organizationID == "" {
			return Candidate{}, ErrForbidden
		}
		viewable, viewErr := s.canViewCandidate(ctx, principal, organizationID)
		if viewErr != nil {
			return Candidate{}, viewErr
		}
		if viewable {
			return Candidate{}, ErrSelfServiceClosed
		}
		return Candidate{}, ErrForbidden
	}
	return candidate, nil
}
