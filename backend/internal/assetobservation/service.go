package assetobservation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/access"
	"netassets/internal/auth"
)

var (
	ErrForbidden   = errors.New("forbidden")
	ErrNotFound    = errors.New("not found")
	ErrInvalid     = errors.New("invalid input")
	ErrUnsupported = errors.New("asset does not support external observation")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Service struct {
	pool        *pgxpool.Pool
	domain      domainLookup
	certificate certificateProbe
	now         func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, domain: newRDAPClient(), certificate: newTLSCollector(), now: time.Now}
}

func (s *Service) List(ctx context.Context, principal auth.Principal, assetID string) ([]Observation, error) {
	if _, err := s.authorizedAsset(ctx, principal, assetID); err != nil {
		return nil, err
	}
	return s.list(ctx, assetID)
}

func (s *Service) Refresh(ctx context.Context, principal auth.Principal, assetID, requestID, ipAddress string) (RefreshResult, error) {
	target, err := s.authorizedAsset(ctx, principal, assetID)
	if err != nil {
		return RefreshResult{}, err
	}
	plans, err := observationPlans(target)
	if err != nil {
		return RefreshResult{}, err
	}

	result := RefreshResult{}
	collectedResults := make([]collectionResult, 0, len(plans))
	for _, plan := range plans {
		collected := s.collect(ctx, plan)
		collectedResults = append(collectedResults, collected)
		switch collected.Status {
		case StatusSuccess:
			result.Successes++
		case StatusUnsupported:
			result.Unsupported++
		default:
			result.Failures++
		}
	}
	if err := s.saveResults(ctx, target.ID, collectedResults); err != nil {
		return RefreshResult{}, err
	}
	items, err := s.list(ctx, target.ID)
	if err != nil {
		return RefreshResult{}, err
	}
	result.Items = items
	if err := s.writeAudit(ctx, principal, target, result, requestID, ipAddress); err != nil {
		return RefreshResult{}, err
	}
	return result, nil
}

type observationPlan struct {
	Type              string
	EndpointKey       string
	Host              string
	Port              string
	UnsupportedReason string
}

func observationPlans(target assetTarget) ([]observationPlan, error) {
	var host, port string
	switch target.AssetType {
	case "domain":
		host = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(target.NormalizedValue), "."))
		port = "443"
	case "url":
		parsed, err := url.Parse(target.NormalizedValue)
		if err != nil || parsed.Hostname() == "" {
			return nil, ErrInvalid
		}
		host = strings.ToLower(parsed.Hostname())
		if parsed.Scheme == "https" {
			port = parsed.Port()
			if port == "" {
				port = "443"
			}
		}
	default:
		return nil, ErrUnsupported
	}
	if net.ParseIP(host) != nil {
		return nil, ErrUnsupported
	}

	root, rootErr := registrableDomain(host)
	plans := make([]observationPlan, 0, 2)
	if rootErr == nil {
		plans = append(plans, observationPlan{Type: TypeDomainRegistration, EndpointKey: root, Host: host})
	}
	if port != "" {
		plan := observationPlan{Type: TypeTLSCertificate, EndpointKey: net.JoinHostPort(host, port), Host: host, Port: port}
		switch {
		case target.NetworkScope != "public":
			plan.UnsupportedReason = "仅公网资产默认启用 TLS 证书探测"
		case strings.HasPrefix(host, "*."):
			plan.UnsupportedReason = "泛域名需要具体主机名才能探测 TLS 证书"
		}
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		return nil, ErrUnsupported
	}
	return plans, nil
}

func (s *Service) collect(ctx context.Context, plan observationPlan) collectionResult {
	if plan.UnsupportedReason != "" {
		return collectionResult{
			ObservationType: plan.Type,
			EndpointKey:     plan.EndpointKey,
			Status:          StatusUnsupported,
			Metadata:        map[string]any{},
			ErrorCode:       "unsupported_target",
			ErrorMessage:    plan.UnsupportedReason,
			RefreshAfter:    30 * 24 * time.Hour,
		}
	}
	operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	switch plan.Type {
	case TypeDomainRegistration:
		lookup, err := s.domain.Lookup(operationCtx, plan.Host)
		if err != nil {
			return failedCollection(plan, classifyCollectionError(err), err)
		}
		return collectionResult{
			ObservationType: TypeDomainRegistration,
			EndpointKey:     lookup.RegisteredDomain,
			Status:          StatusSuccess,
			Source:          "rdap",
			SourceReference: lookup.SourceReference,
			ExpiresAt:       &lookup.ExpiresAt,
			Metadata:        lookup.Metadata,
			RefreshAfter:    refreshInterval(lookup.ExpiresAt, s.now()),
		}
	case TypeTLSCertificate:
		certificate, err := s.certificate.Probe(operationCtx, plan.Host, plan.Port)
		if err != nil {
			return failedCollection(plan, classifyCollectionError(err), err)
		}
		return collectionResult{
			ObservationType: TypeTLSCertificate,
			EndpointKey:     plan.EndpointKey,
			Status:          StatusSuccess,
			Source:          "tls_handshake",
			SourceReference: certificate.SourceReference,
			NotBefore:       &certificate.NotBefore,
			ExpiresAt:       &certificate.ExpiresAt,
			Metadata:        certificate.Metadata,
			RefreshAfter:    refreshInterval(certificate.ExpiresAt, s.now()),
		}
	default:
		return failedCollection(plan, "unsupported_observation", ErrUnsupported)
	}
}

func refreshInterval(expiresAt, now time.Time) time.Duration {
	if expiresAt.Before(now.Add(30 * 24 * time.Hour)) {
		return 6 * time.Hour
	}
	return 24 * time.Hour
}

func failedCollection(plan observationPlan, code string, err error) collectionResult {
	message := strings.TrimSpace(err.Error())
	if len(message) > 1000 {
		message = message[:1000]
	}
	return collectionResult{
		ObservationType: plan.Type,
		EndpointKey:     plan.EndpointKey,
		Status:          StatusFailed,
		Metadata:        map[string]any{},
		ErrorCode:       code,
		ErrorMessage:    message,
		RefreshAfter:    15 * time.Minute,
	}
}

func classifyCollectionError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errNoPublicAddress):
		return "non_public_address"
	case errors.Is(err, errRDAPUnsupported):
		return "rdap_unsupported"
	case errors.Is(err, errRDAPNoExpiry):
		return "rdap_expiry_missing"
	case errors.Is(err, errTLSNoCertificate):
		return "tls_no_certificate"
	default:
		return "collection_failed"
	}
}

func (s *Service) authorizedAsset(ctx context.Context, principal auth.Principal, assetID string) (assetTarget, error) {
	assetID = strings.TrimSpace(assetID)
	if !uuidPattern.MatchString(assetID) {
		return assetTarget{}, ErrInvalid
	}
	if !principal.HasPermission("asset.manage") {
		return assetTarget{}, ErrForbidden
	}
	var target assetTarget
	err := s.pool.QueryRow(ctx, `
		SELECT a.id::text,a.asset_type,a.asset_value,a.normalized_value,a.network_scope,ao.organization_id::text
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
		WHERE a.id=$1::uuid AND a.deleted_at IS NULL
	`, assetID).Scan(&target.ID, &target.AssetType, &target.Value, &target.NormalizedValue, &target.NetworkScope, &target.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return assetTarget{}, ErrNotFound
	}
	if err != nil {
		return assetTarget{}, fmt.Errorf("load observation asset: %w", err)
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, target.OrganizationID, "asset.manage")
	if err != nil {
		return assetTarget{}, err
	}
	if !allowed {
		return assetTarget{}, ErrForbidden
	}
	return target, nil
}

func (s *Service) saveResults(ctx context.Context, assetID string, results []collectionResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin asset observation save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	hasDomainRegistration := false
	hasTLSCertificate := false
	for _, result := range results {
		switch result.ObservationType {
		case TypeDomainRegistration:
			hasDomainRegistration = true
		case TypeTLSCertificate:
			hasTLSCertificate = true
		}
		if err := saveResult(ctx, tx, assetID, result); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM asset_external_observations
		WHERE asset_id=$1::uuid
		  AND ((observation_type='domain_registration' AND NOT $2)
		    OR (observation_type='tls_certificate' AND NOT $3))
	`, assetID, hasDomainRegistration, hasTLSCertificate); err != nil {
		return fmt.Errorf("remove unplanned asset observations: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit asset observation save: %w", err)
	}
	return nil
}

func saveResult(ctx context.Context, tx pgx.Tx, assetID string, result collectionResult) error {
	metadata, err := json.Marshal(result.Metadata)
	if err != nil {
		return fmt.Errorf("encode observation metadata: %w", err)
	}
	refreshAfter := result.RefreshAfter
	if refreshAfter <= 0 {
		refreshAfter = 24 * time.Hour
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM asset_external_observations
		WHERE asset_id=$1::uuid AND observation_type=$2 AND endpoint_key<>$3
	`, assetID, result.ObservationType, result.EndpointKey); err != nil {
		return fmt.Errorf("remove stale asset observation endpoint: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_external_observations(
			asset_id,observation_type,endpoint_key,status,source,source_reference,not_before,expires_at,
			metadata,last_attempt_at,last_success_at,next_refresh_at,consecutive_failures,error_code,error_message
		) VALUES(
			$1::uuid,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,$8,$9::jsonb,now(),
			CASE WHEN $4='success' THEN now() ELSE NULL END,now()+$10::interval,
			CASE WHEN $4='failed' THEN 1 ELSE 0 END,NULLIF($11,''),NULLIF($12,'')
		)
		ON CONFLICT(asset_id,observation_type,endpoint_key) DO UPDATE SET
			status=EXCLUDED.status,
			source=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.source ELSE asset_external_observations.source END,
			source_reference=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.source_reference ELSE asset_external_observations.source_reference END,
			not_before=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.not_before ELSE asset_external_observations.not_before END,
			expires_at=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.expires_at ELSE asset_external_observations.expires_at END,
			metadata=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.metadata ELSE asset_external_observations.metadata END,
			last_attempt_at=now(),
			last_success_at=CASE WHEN EXCLUDED.status='success' THEN now() ELSE asset_external_observations.last_success_at END,
			next_refresh_at=EXCLUDED.next_refresh_at,
			consecutive_failures=CASE WHEN EXCLUDED.status='failed' THEN asset_external_observations.consecutive_failures+1 ELSE 0 END,
			error_code=EXCLUDED.error_code,error_message=EXCLUDED.error_message,updated_at=now()
	`, assetID, result.ObservationType, result.EndpointKey, result.Status, result.Source, result.SourceReference,
		result.NotBefore, result.ExpiresAt, string(metadata), intervalLiteral(refreshAfter), result.ErrorCode, result.ErrorMessage); err != nil {
		return fmt.Errorf("save asset observation: %w", err)
	}
	return nil
}

func intervalLiteral(duration time.Duration) string {
	seconds := int64(duration / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func (s *Service) list(ctx context.Context, assetID string) ([]Observation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,asset_id::text,observation_type,endpoint_key,status,COALESCE(source,''),COALESCE(source_reference,''),
		       not_before,expires_at,metadata,last_attempt_at,last_success_at,next_refresh_at,consecutive_failures,
		       COALESCE(error_code,''),COALESCE(error_message,''),created_at,updated_at
		FROM asset_external_observations
		WHERE asset_id=$1::uuid
		ORDER BY CASE observation_type WHEN 'domain_registration' THEN 1 ELSE 2 END,endpoint_key
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("list asset observations: %w", err)
	}
	defer rows.Close()
	items := make([]Observation, 0)
	for rows.Next() {
		var item Observation
		var rawMetadata []byte
		if err := rows.Scan(&item.ID, &item.AssetID, &item.ObservationType, &item.EndpointKey, &item.Status, &item.Source,
			&item.SourceReference, &item.NotBefore, &item.ExpiresAt, &rawMetadata, &item.LastAttemptAt, &item.LastSuccessAt,
			&item.NextRefreshAt, &item.ConsecutiveFailures, &item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan asset observation: %w", err)
		}
		if err := json.Unmarshal(rawMetadata, &item.Metadata); err != nil {
			return nil, fmt.Errorf("decode asset observation metadata: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) writeAudit(ctx context.Context, principal auth.Principal, target assetTarget, result RefreshResult, requestID, ipAddress string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'asset.external_observation.refresh','asset',$3,'刷新资产域名及证书信息',
		       jsonb_build_object('successes',$4::int,'failures',$5::int,'unsupported',$6::int),
		       NULLIF($7,'')::inet,NULLIF($8,''))
	`, principal.UserID, target.OrganizationID, target.ID, result.Successes, result.Failures, result.Unsupported, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write asset observation audit: %w", err)
	}
	return nil
}
