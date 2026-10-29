package dataquality

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const exemptionExpiryPollInterval = time.Minute

// StartExemptionExpiry is independent of automatic rule scans: an exemption must
// expire even when scans are disabled or its rule no longer matches the resource.
func (s *Service) StartExemptionExpiry(ctx context.Context, gate AutoScanWriteGate, logger *slog.Logger) {
	if s == nil || s.pool == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	go s.runExemptionExpiry(ctx, gate, logger, exemptionExpiryPollInterval)
}

func (s *Service) runExemptionExpiry(ctx context.Context, gate AutoScanWriteGate, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	attempt := func() {
		if ctx.Err() != nil {
			return
		}
		if gate != nil {
			if !gate.TryEnterWrite() {
				return
			}
			defer gate.LeaveWrite()
		}
		expiryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// Bound each transaction to avoid holding many locks; further batches are
		// handled at the next tick. Concurrent app instances skip each other's rows.
		count, err := s.expireExemptions(expiryCtx, time.Now())
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("expire data quality exemptions", "error", err)
			}
		} else if count > 0 {
			logger.Info("expired data quality exemptions reopened", "count", count)
		}
	}
	attempt()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attempt()
		}
	}
}

// expireExemptions atomically reopens a bounded set of expired issues and records
// both their original exemption metadata and a system audit entry. No user is
// impersonated, and concurrent workers cannot record the same transition twice.
func (s *Service) expireExemptions(ctx context.Context, cutoff time.Time) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `
  WITH due AS MATERIALIZED (
   SELECT id,exemption_reason,exemption_expires_at
   FROM data_quality_issues
   WHERE status='exempted' AND exemption_expires_at IS NOT NULL AND exemption_expires_at<=$1
   ORDER BY exemption_expires_at,id
   LIMIT 500 FOR UPDATE SKIP LOCKED
  ), reopened AS (
   UPDATE data_quality_issues i SET
    status='pending',resolution_type=NULL,closed_at=NULL,
    exemption_reason=NULL,exemption_expires_at=NULL,
    review_note=NULL,reviewed_by=NULL,reviewed_at=NULL,
    updated_by=NULL,updated_at=now()
   FROM due WHERE i.id=due.id
   RETURNING i.id,i.organization_id,due.exemption_reason,due.exemption_expires_at
  ), events AS (
   INSERT INTO data_quality_issue_events(issue_id,event_type,from_status,to_status,note,details)
   SELECT id,'reopened','exempted','pending','豁免有效期已结束，系统自动恢复待处理',
    jsonb_build_object('reason','exemption_expired','exemption_reason',exemption_reason,'exemption_expires_at',exemption_expires_at)
   FROM reopened
  ), audits AS (
   INSERT INTO audit_logs(organization_id,action,resource_type,resource_id,summary,details)
   SELECT organization_id,'data_quality_issue.exemption_expired','data_quality_issue',id::text,
    '数据质量问题豁免到期，系统自动恢复待处理',
    jsonb_build_object('from_status','exempted','to_status','pending','exemption_reason',exemption_reason,'exemption_expires_at',exemption_expires_at)
   FROM reopened
  ) SELECT count(*) FROM reopened
 `, cutoff).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("expire data quality exemptions: %w", err)
	}
	return count, nil
}
