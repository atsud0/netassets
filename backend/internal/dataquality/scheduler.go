package dataquality

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	autoScanAdvisoryLockKey int64 = 724751840137
	autoScanPollInterval          = time.Minute
	autoScanRetryInterval         = 15 * time.Minute
	autoScanTimeout               = 2 * time.Hour
)

var (
	errScheduledScanLocked    = errors.New("scheduled data quality scan is locked by another instance")
	errScheduledScanCompleted = errors.New("scheduled data quality scan already completed")
)

type AutoScanWriteGate interface {
	TryEnterWrite() bool
	LeaveWrite()
}

type AutoScanSchedule struct {
	Enabled  bool
	Hour     int
	Minute   int
	Timezone string
	location *time.Location
}

func NewAutoScanSchedule(enabled bool, hour, minute int, timezone string) (AutoScanSchedule, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return AutoScanSchedule{}, fmt.Errorf("invalid automatic data quality scan time %02d:%02d", hour, minute)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return AutoScanSchedule{}, fmt.Errorf("load automatic data quality scan timezone: %w", err)
	}
	return AutoScanSchedule{Enabled: enabled, Hour: hour, Minute: minute, Timezone: timezone, location: location}, nil
}

func (s *Service) StartAutoScan(ctx context.Context, schedule AutoScanSchedule, gate AutoScanWriteGate, logger *slog.Logger) {
	if s == nil || s.pool == nil || !schedule.Enabled {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	if schedule.location == nil {
		location, err := time.LoadLocation(schedule.Timezone)
		if err != nil {
			logger.Error("start automatic data quality scan", "error", err)
			return
		}
		schedule.location = location
	}
	go s.runAutoScan(ctx, schedule, gate, logger)
}

func (s *Service) runAutoScan(ctx context.Context, schedule AutoScanSchedule, gate AutoScanWriteGate, logger *slog.Logger) {
	ticker := time.NewTicker(autoScanPollInterval)
	defer ticker.Stop()

	completedDate := ""
	retryDate := ""
	var retryAfter time.Time
	attempt := func(now time.Time) {
		scheduledFor, due := schedule.dueDate(now)
		if !due || scheduledFor == completedDate {
			return
		}
		if retryDate == scheduledFor && now.Before(retryAfter) {
			return
		}

		if gate != nil && !gate.TryEnterWrite() {
			logger.Info("automatic data quality scan deferred while backup is running", "scheduled_for", scheduledFor)
			return
		}
		result, err := func() (ScanResult, error) {
			if gate != nil {
				defer gate.LeaveWrite()
			}
			scanCtx, cancel := context.WithTimeout(ctx, autoScanTimeout)
			defer cancel()
			return s.scan(scanCtx, "", ScanFilter{}, scanTrigger{Type: scanTriggerScheduled, ScheduledFor: scheduledFor}, "scheduled-data-quality-scan-"+scheduledFor, "")
		}()
		switch {
		case err == nil:
			completedDate = scheduledFor
			retryDate = ""
			logger.Info("automatic data quality scan completed",
				"scheduled_for", scheduledFor,
				"batch_id", result.BatchID,
				"issue_count", result.Summary.TotalIssueCount,
				"auto_closed_count", result.Summary.AutoClosedCount,
			)
		case errors.Is(err, errScheduledScanCompleted):
			completedDate = scheduledFor
			retryDate = ""
		case errors.Is(err, errScheduledScanLocked), errors.Is(err, context.Canceled):
			return
		default:
			retryDate = scheduledFor
			retryAfter = now.Add(autoScanRetryInterval)
			logger.Error("automatic data quality scan failed", "scheduled_for", scheduledFor, "error", err)
			s.recordScheduledScanFailure(ctx, scheduledFor)
		}
	}

	attempt(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			attempt(now)
		}
	}
}

func (schedule AutoScanSchedule) dueDate(now time.Time) (string, bool) {
	if !schedule.Enabled || schedule.location == nil {
		return "", false
	}
	localNow := now.In(schedule.location)
	if localNow.Hour() < schedule.Hour || (localNow.Hour() == schedule.Hour && localNow.Minute() < schedule.Minute) {
		return "", false
	}
	return localNow.Format("2006-01-02"), true
}

func (s *Service) recordScheduledScanFailure(ctx context.Context, scheduledFor string) {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(auditCtx, `
		INSERT INTO audit_logs(user_id,action,resource_type,summary,details,request_id,result)
		VALUES(NULL,'data_quality.scan.scheduled','data_quality_scan_batch','系统自动数据质量检测执行失败',
		       jsonb_build_object('trigger_type','scheduled','scheduled_for',$1::date,'error_code','scan_failed'),$2,'failure')
	`, scheduledFor, "scheduled-data-quality-scan-"+scheduledFor)
}
