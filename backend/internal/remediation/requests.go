package remediation

import (
	"context"
	"strings"
	"time"

	"netassets/internal/auth"
)

type RequestInput struct {
	Type            string     `json:"type"`
	Reason          string     `json:"reason"`
	Mitigation      string     `json:"mitigation"`
	ProposedDueAt   *time.Time `json:"proposed_due_at"`
	ExpectedVersion int        `json:"expected_version"`
}

func (s *Service) Request(ctx context.Context, p auth.Principal, id string, in RequestInput) (Object, error) {
	if !contains([]string{"extension", "reassign", "false_positive", "risk_acceptance"}, in.Type) {
		return nil, invalidField("申请类型：请选择延期、转派、误报或风险接受")
	}
	if !textOK(in.Reason, 10000, true) {
		return nil, invalidField("申请原因：请输入 1 至 10000 字")
	}
	if !textOK(in.Mitigation, 5000, false) {
		return nil, invalidField("缓解措施：最多 5000 字")
	}
	if in.Type == "extension" {
		if !textOK(in.Mitigation, 5000, true) {
			return nil, invalidField("延期缓解措施：请输入 1 至 5000 字")
		}
		if in.ProposedDueAt == nil || !in.ProposedDueAt.After(time.Now()) {
			return nil, invalidField("拟延期截止时间：须晚于当前时间")
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t, err := lockTask(ctx, tx, p, id, true)
	if err != nil {
		return nil, err
	}
	if t.Version != in.ExpectedVersion || !contains([]string{"pending_acceptance", "in_progress"}, t.Status) {
		return nil, ErrConflict
	}
	var requestID string
	err = tx.QueryRow(ctx, `INSERT INTO remediation_requests(task_id,author_id,type,reason,proposed_due_at,mitigation) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6) RETURNING id::text`, id, p.UserID, in.Type, in.Reason, in.ProposedDueAt, in.Mitigation).Scan(&requestID)
	if err != nil {
		return nil, dbError(err)
	}
	if err = event(ctx, tx, p, t, "request.create", "participants", Object{"type": in.Type, "reason": in.Reason}); err != nil {
		return nil, err
	}
	return Object{"id": requestID}, tx.Commit(ctx)
}

type DecideInput struct {
	Decision        string     `json:"decision"`
	Reason          string     `json:"reason"`
	GrantID         string     `json:"grant_id"`
	RiskUntil       *time.Time `json:"risk_accepted_until"`
	ExpectedVersion int        `json:"expected_version"`
}

func (s *Service) Decide(ctx context.Context, p auth.Principal, id string, in DecideInput) error {
	if !validID(id) {
		return invalidField("整改申请标识不正确")
	}
	if !contains([]string{"approved", "rejected"}, in.Decision) {
		return invalidField("审批结论：请选择批准或拒绝")
	}
	if !textOK(in.Reason, 10000, true) {
		return invalidField("审批原因：请输入 1 至 10000 字")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var taskID, kind, state string
	var proposed *time.Time
	err = tx.QueryRow(ctx, `SELECT task_id::text,type,status,proposed_due_at FROM remediation_requests WHERE id=$1::uuid`, id).Scan(&taskID, &kind, &state, &proposed)
	if err != nil {
		return dbError(err)
	}
	t, err := lockTask(ctx, tx, p, taskID, false)
	if err != nil {
		return err
	}
	if state != "pending" || t.Version != in.ExpectedVersion || !contains([]string{"pending_acceptance", "in_progress"}, t.Status) {
		return ErrConflict
	}
	group := security(ctx, tx, p)
	if kind == "false_positive" || kind == "risk_acceptance" {
		if !group {
			return ErrForbidden
		}
	} else if !group {
		if err = allowed(ctx, tx, p, t.OrganizationID, "remediation.assign"); err != nil {
			return err
		}
	}
	if in.Decision == "approved" {
		switch kind {
		case "extension":
			if proposed == nil || !proposed.After(t.DueAt) || !proposed.After(time.Now()) {
				return invalidField("拟延期截止时间：须晚于原截止时间及当前时间")
			}
			var orderDue, grantUntil time.Time
			if err = tx.QueryRow(ctx, `SELECT w.due_at,g.valid_until FROM security_work_orders w JOIN remediation_grants g ON g.id=$2::uuid WHERE w.id=$1::uuid`, t.WorkOrderID, t.GrantID).Scan(&orderDue, &grantUntil); err != nil {
				return err
			}
			if proposed.After(grantUntil) {
				return invalidField("拟延期截止时间：不能超过执行人的授权有效期")
			}
			if proposed.After(orderDue) {
				if !group {
					return ErrForbidden
				}
				if _, err = tx.Exec(ctx, `UPDATE security_work_orders SET due_at=$2,updated_at=now() WHERE id=$1::uuid`, t.WorkOrderID, proposed); err != nil {
					return err
				}
				if err = audit(ctx, tx, p, t.OrganizationID, t.WorkOrderID, "deadline.extend", Object{"old_due_at": orderDue, "new_due_at": proposed, "reason": in.Reason}); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET due_at=$2 WHERE id=$1::uuid`, t.ID, proposed)
		case "reassign":
			if !validID(in.GrantID) {
				return invalidField("转派执行人：请选择有效的人员授权")
			}
			var assignee string
			if err = tx.QueryRow(ctx, `SELECT user_id::text FROM remediation_grants WHERE id=$1::uuid AND remediation_grant_valid(id,user_id,$2::uuid,$3::uuid) AND valid_until >= $4`, in.GrantID, t.OrganizationID, t.SystemID, t.DueAt).Scan(&assignee); err != nil {
				return dbError(err)
			}
			_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET assignee_user_id=$2::uuid,grant_id=$3::uuid,status='pending_acceptance',accepted_at=NULL,expected_completion_at=NULL,blocked_reason='',blocked_dependency='',blocked_until=NULL WHERE id=$1::uuid`, t.ID, assignee, in.GrantID)
			if err == nil {
				err = event(ctx, tx, p, t, "reassign", "participants", Object{"from_user_id": t.AssigneeID, "to_user_id": assignee, "reason": in.Reason})
			}
		case "false_positive", "risk_acceptance":
			outcome := "false_positive"
			if kind == "risk_acceptance" {
				outcome = "risk_accepted"
				if in.RiskUntil == nil || !in.RiskUntil.After(time.Now()) {
					return invalidField("风险接受有效期：须晚于当前时间")
				}
			}
			if kind == "risk_acceptance" {
				_, err = tx.Exec(ctx, `UPDATE security_findings SET risk_accepted_from_status=status,status='risk_accepted',risk_accepted_at=now(),risk_accepted_until=$2,risk_acceptance_reason=$3,risk_accepted_by=$4::uuid,updated_at=now(),updated_by=$4::uuid WHERE id=$1::uuid`, t.FindingID, in.RiskUntil, in.Reason, p.UserID)
			} else {
				_, err = tx.Exec(ctx, `UPDATE security_findings SET status='false_positive',updated_at=now(),updated_by=$2::uuid WHERE id=$1::uuid`, t.FindingID, p.UserID)
			}
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET status='completed',completion_reason=$2,completed_at=now() WHERE id=$1::uuid`, t.ID, outcome)
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_requests SET status=$2,decision=$3,decided_by=$4::uuid,decided_at=now() WHERE id=$1::uuid`, id, in.Decision, strings.TrimSpace(in.Reason), p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_tasks SET version=version+1,updated_at=now() WHERE id=$1::uuid`, t.ID); err != nil {
		return err
	}
	after, err := loadTask(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if err = event(ctx, tx, p, t, "request.decide", "participants", Object{"type": kind, "decision": in.Decision, "reason": in.Reason, "from_status": t.Status, "to_status": after.Status, "old_due_at": t.DueAt, "new_due_at": after.DueAt}); err != nil {
		return err
	}
	if err = refreshOrder(ctx, tx, t.WorkOrderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
