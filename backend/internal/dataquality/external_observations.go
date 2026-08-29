package dataquality

import (
	"context"
	"fmt"
	"time"

	"netassets/internal/assetobservation"
)

const (
	externalRefreshManualLimit      = 100
	externalRefreshScheduledLimit   = 1000
	externalRefreshConcurrency      = 8
	externalRefreshManualTimeout    = 25 * time.Second
	externalRefreshScheduledTimeout = 10 * time.Minute
)

var externalExpiryTargets = map[string]string{
	"domain_registration_expires_at": assetobservation.TypeDomainRegistration,
	"tls_certificate_expires_at":     assetobservation.TypeTLSCertificate,
}

type externalObservationRefresher interface {
	RefreshDue(context.Context, assetobservation.BulkRefreshOptions) (assetobservation.BulkRefreshResult, error)
}

func isExternalExpiryTarget(target string) bool {
	_, ok := externalExpiryTargets[target]
	return ok
}

func (s *Service) refreshExternalObservations(ctx context.Context, filter ScanFilter, trigger scanTrigger) (assetobservation.BulkRefreshResult, error) {
	if s.observationRefresher == nil || filter.ResourceType == "business_system" {
		return assetobservation.BulkRefreshResult{}, nil
	}
	var enabled bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM data_quality_rules
			WHERE is_enabled AND resource_type='asset' AND rule_type='expiry'
			  AND target_field IN ('domain_registration_expires_at','tls_certificate_expires_at')
			  AND (NULLIF($1::text,'') IS NULL OR severity=$1)
		)
	`, filter.Severity).Scan(&enabled); err != nil {
		return assetobservation.BulkRefreshResult{}, fmt.Errorf("check external expiry quality rules: %w", err)
	}
	if !enabled {
		return assetobservation.BulkRefreshResult{}, nil
	}

	limit := externalRefreshManualLimit
	timeout := externalRefreshManualTimeout
	if trigger.Type == scanTriggerScheduled {
		limit = externalRefreshScheduledLimit
		timeout = externalRefreshScheduledTimeout
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := s.observationRefresher.RefreshDue(refreshCtx, assetobservation.BulkRefreshOptions{
		OrganizationID: filter.OrganizationID,
		Limit:          limit,
		Concurrency:    externalRefreshConcurrency,
	})
	if err != nil {
		return result, fmt.Errorf("refresh external asset observations: %w", err)
	}
	return result, nil
}
