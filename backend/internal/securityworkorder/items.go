package securityworkorder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"netassets/internal/auth"
	"netassets/internal/textlength"
)

type WorkOrderItemInput struct {
	Items       []WorkOrderItemVersion `json:"items"`
	Action      string                 `json:"action"`
	Note        string                 `json:"note"`
	EvidenceIDs []string               `json:"evidence_ids"`
}

type WorkOrderItemVersion struct {
	ID               string     `json:"id"`
	Version          int64      `json:"version"`
	FindingUpdatedAt *time.Time `json:"finding_updated_at"`
}

func nextItemStage(stage, action string) (string, error) {
	switch action {
	case "progress":
		if contains([]string{"not_started", "in_progress", "returned"}, stage) {
			return "in_progress", nil
		}
	case "submit":
		if contains([]string{"not_started", "in_progress", "returned"}, stage) {
			return "pending_verification", nil
		}
	case "pass":
		if stage == "pending_verification" {
			return "verified", nil
		}
	case "return":
		if stage == "pending_verification" {
			return "returned", nil
		}
	}
	return "", ErrState
}

func (s *Service) ProcessWorkOrderItems(ctx context.Context, principal auth.Principal, orderID string, input WorkOrderItemInput, requestID, ipAddress string) (BatchResult, error) {
	if !validUUID(orderID) {
		return BatchResult{}, ErrInvalid
	}
	if !contains([]string{"progress", "submit", "pass", "return"}, input.Action) {
		return BatchResult{}, ErrInvalid
	}
	input.Note = strings.TrimSpace(input.Note)
	if input.Note == "" || textlength.Count(input.Note) > 5000 {
		return BatchResult{}, invalidField("处理说明必填，最多 5000 字")
	}
	if len(input.EvidenceIDs) > 20 {
		return BatchResult{}, invalidField("每次最多关联 20 个材料附件")
	}
	seenEvidence := map[string]bool{}
	for _, id := range input.EvidenceIDs {
		if !validUUID(id) || seenEvidence[strings.ToLower(id)] {
			return BatchResult{}, invalidField("附件标识无效或重复")
		}
		seenEvidence[strings.ToLower(id)] = true
	}
	if input.Action == "submit" && len(input.EvidenceIDs) == 0 {
		return BatchResult{}, invalidField("提交复测必须关联至少一个整改材料附件")
	}
	items := make([]BatchItem, 0, len(input.Items))
	versions := map[string]WorkOrderItemVersion{}
	for _, item := range input.Items {
		items = append(items, BatchItem{ID: item.ID})
		versions[item.ID] = item
	}
	if err := validateBatch(BatchInput{Items: items}); err != nil {
		return BatchResult{}, err
	}
	if _, err := s.GetWorkOrder(ctx, principal, orderID); err != nil {
		return BatchResult{}, err
	}
	return runBatch(ctx, items, func(item BatchItem) error {
		return s.processWorkOrderItem(ctx, principal, orderID, versions[item.ID], input, requestID, ipAddress)
	}), nil
}

func (s *Service) processWorkOrderItem(ctx context.Context, principal auth.Principal, orderID string, item WorkOrderItemVersion, input WorkOrderItemInput, requestID, ipAddress string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return err
	}
	var orderStatus, organizationID, mode string
	err = tx.QueryRow(ctx, `SELECT status,assignee_organization_id::text,execution_mode FROM security_work_orders WHERE id=$1::uuid FOR UPDATE`, orderID).Scan(&orderStatus, &organizationID, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	action := "progress"
	if input.Action == "submit" {
		action = "submit"
	}
	if input.Action == "pass" || input.Action == "return" {
		if !principal.CanManageSecurityWorkOrders() {
			return ErrForbidden
		}
	} else if !principal.CanExecuteSecurityWorkOrders() && !principal.CanManageSecurityWorkOrders() {
		return ErrForbidden
	}
	if err = s.authorizeWorkOrderAction(ctx, principal, organizationID, mode, action); err != nil {
		return err
	}
	if mode != "organization" {
		return ErrIndividualWorkOrderAction
	}
	if !contains([]string{"in_progress", "returned", "pending_verification"}, orderStatus) {
		return ErrState
	}
	var stage, status, findingOrganization, findingCode string
	var version int64
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `SELECT link.stage,link.version,f.status,f.updated_at,f.assignee_organization_id::text,f.finding_code
 FROM security_work_order_findings link JOIN security_findings f ON f.id=link.finding_id
	 WHERE link.work_order_id=$1::uuid AND link.finding_id=$2::uuid FOR UPDATE OF link,f`, orderID, item.ID).Scan(&stage, &version, &status, &updatedAt, &findingOrganization, &findingCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if findingOrganization != organizationID {
		return ErrConflict
	}
	if item.Version <= 0 || item.FindingUpdatedAt == nil {
		return ErrVersionRequired
	}
	if version != item.Version {
		return ErrEditConflict
	}
	if err = checkFindingVersion(item.ID, item.FindingUpdatedAt, updatedAt); err != nil {
		return err
	}
	next, err := nextItemStage(stage, input.Action)
	if err != nil {
		return err
	}
	if input.Action == "pass" || input.Action == "return" {
		if status != "resolved" {
			return ErrState
		}
	} else if !contains([]string{"pending", "confirmed", "in_progress"}, status) && !(input.Action == "submit" && stage == "not_started" && status == "resolved") {
		return ErrState
	}
	evidenceIDs := input.EvidenceIDs
	if evidenceIDs == nil {
		evidenceIDs = []string{}
	}
	if len(evidenceIDs) > 0 {
		var count int
		rows, queryErr := tx.Query(ctx, `SELECT id FROM security_work_order_attachments WHERE work_order_id=$1::uuid AND id=ANY($2::uuid[]) AND category=$3 FOR SHARE`, orderID, evidenceIDs, func() string {
			if input.Action == "pass" || input.Action == "return" {
				return "verification"
			}
			return "remediation"
		}())
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			count++
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		if count != len(evidenceIDs) {
			return invalidField("附件必须是本工单对应类别的有效材料")
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE security_work_order_findings SET stage=$3,version=version+1,processing_note=$4,evidence_ids=$5::uuid[],processed_at=clock_timestamp(),processed_by=$6::uuid WHERE work_order_id=$1::uuid AND finding_id=$2::uuid`, orderID, item.ID, next, input.Note, evidenceIDs, principal.UserID); err != nil {
		return err
	}
	nextStatus := "in_progress"
	if input.Action == "submit" {
		nextStatus = "resolved"
	}
	if input.Action == "pass" {
		nextStatus = "fixed"
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('netassets.order_item_write','on',true)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE security_findings SET status=$2,updated_at=clock_timestamp(),updated_by=$3::uuid,
 remediation_status=$4 WHERE id=$1::uuid`, item.ID, nextStatus, principal.UserID, func() string {
		if next == "verified" {
			return "completed"
		}
		if next == "pending_verification" {
			return "pending_security_review"
		}
		return "in_progress"
	}()); err != nil {
		return fmt.Errorf("update work order finding: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO security_work_order_item_history(work_order_id,finding_id,version,action,from_stage,to_stage,note,evidence_ids,actor_id)
 VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8::uuid[],$9::uuid)`, orderID, item.ID, version+1, input.Action, stage, next, input.Note, evidenceIDs, principal.UserID); err != nil {
		return err
	}
	details := map[string]any{"finding_id": item.ID, "finding_code": findingCode, "note": input.Note, "version": version + 1, "evidence_ids": evidenceIDs, "from_status": status, "to_status": nextStatus, "from_stage": stage, "to_stage": next}
	if err = writeEvent(ctx, tx, principal, orderID, "item_"+input.Action, stage, next, findingCode+": "+input.Note, details); err != nil {
		return err
	}
	if err = writeAudit(ctx, tx, principal, organizationID, "security_work_order.item_"+input.Action, "security_work_order", orderID, "工单关联发现逐项处置", details, requestID, ipAddress); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT sync_organization_order($1::uuid,$2::uuid)`, orderID, principal.UserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
