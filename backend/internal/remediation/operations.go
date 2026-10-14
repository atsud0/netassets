package remediation

import (
	"context"
	"time"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func (s *Service) UpdateSource(ctx context.Context, p auth.Principal, id string, in ActionInput, executor bool) (Object, error) {
	if executor || !security(ctx, s.pool, p) {
		return nil, ErrForbidden
	}
	if !validID(id) || !textOK(in.Reason, 2000, true) || !textOK(in.Material.Title, 255, true) || !textOK(in.Material.Description, 20000, true) || !contains([]string{"critical", "high", "medium", "low", "info"}, in.Material.Severity) || !textOK(in.Material.Evidence, 20000, false) || !textOK(in.Material.Advice, 20000, false) {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t, err := lockTask(ctx, tx, p, id, false)
	if err != nil {
		return nil, err
	}
	if t.Version != in.ExpectedVersion || contains([]string{"completed", "cancelled"}, t.Status) {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE security_findings SET title=$2,severity=$3,description=$4,evidence=$5,remediation_advice=$6,updated_at=now(),updated_by=$7::uuid WHERE id=$1::uuid`, t.FindingID, in.Material.Title, in.Material.Severity, in.Material.Description, in.Material.Evidence, in.Material.Advice, p.UserID); err != nil {
		return nil, err
	}
	// Only internal viewers receive the source-edit reason; the executor sees a neutral review flag.
	if err = event(ctx, tx, p, t, "source-update", "internal", Object{"reason": in.Reason}); err != nil {
		return nil, err
	}
	current, err := loadTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return Object{"id": id, "version": current.Version, "material_needs_review": current.MaterialNeedsReview}, tx.Commit(ctx)
}

func (s *Service) Statistics(ctx context.Context, p auth.Principal, in Filter) (Object, error) {
	sql, args, err := taskQuery(p, in)
	if err != nil {
		return nil, err
	}
	return readObject(ctx, s.pool, `WITH visible AS (`+sql+`), timeline AS (
 SELECT e.task_id,e.created_at,COALESCE(e.details->>'to_status',CASE WHEN e.action='assign' THEN 'pending_acceptance' END) state,
 lead(e.created_at,1,now()) OVER(PARTITION BY e.task_id ORDER BY e.created_at,e.id) ended
 FROM remediation_task_events e JOIN visible v ON v.id=e.task_id WHERE e.details ? 'to_status' OR e.action='assign'
 ), durations AS (SELECT task_id,
 sum(extract(epoch FROM ended-created_at)) FILTER(WHERE state IN ('pending_acceptance','in_progress')) execution_seconds,
 sum(extract(epoch FROM ended-created_at)) FILTER(WHERE state='pending_org_review') organization_seconds,
 sum(extract(epoch FROM ended-created_at)) FILTER(WHERE state='pending_security_review') security_seconds
 FROM timeline GROUP BY task_id)
 SELECT count(*) total,count(*) FILTER(WHERE v.completion_reason='verified_fixed') verified_fixed,
 count(*) FILTER(WHERE v.completion_reason='risk_accepted' AND v.risk_acceptance_current) risk_accepted,count(*) FILTER(WHERE v.completion_reason='risk_accepted' AND NOT v.risk_acceptance_current) risk_expired,count(*) FILTER(WHERE v.completion_reason='false_positive') false_positive,
 count(*) FILTER(WHERE v.overdue) overdue,count(*) FILTER(WHERE v.due_at<>v.original_due_at) extended,
 count(*) FILTER(WHERE remediation_task_recently_revived(v.id)) revived,
 COALESCE(avg(d.execution_seconds),0) execution_seconds,COALESCE(avg(d.organization_seconds),0) organization_seconds,COALESCE(avg(d.security_seconds),0) security_seconds,
 COALESCE((SELECT jsonb_object_agg(status,n) FROM(SELECT status,count(*) n FROM visible GROUP BY status) x),'{}'::jsonb) counts,
 COALESCE((SELECT jsonb_object_agg(reason,n) FROM(SELECT execution_blocked_reason reason,count(*) n FROM visible WHERE execution_blocked_reason IS NOT NULL GROUP BY execution_blocked_reason) x),'{}'::jsonb) block_reasons,
 COALESCE((SELECT jsonb_agg(x) FROM(SELECT organization_name,assignee_name,vendor_name,count(*) total,count(*) FILTER(WHERE status NOT IN ('completed','cancelled')) active FROM visible GROUP BY organization_name,assignee_name,vendor_name ORDER BY active DESC,assignee_name) x),'[]'::jsonb) workload
 FROM visible v LEFT JOIN durations d ON d.task_id=v.id`, args...)
}

func (s *Service) Catalog(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Object], error) {
	if !groupAdmin(ctx, s.pool, p) {
		return pagination.Result[Object]{}, ErrForbidden
	}
	return pageObjects(ctx, s.pool, `SELECT c.id,c.name,c.status,(SELECT count(*) FROM vendors v WHERE v.catalog_id=c.id) organization_count FROM vendor_catalog c ORDER BY c.name,c.id`, page)
}

type CatalogInput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (s *Service) SaveCatalog(ctx context.Context, p auth.Principal, id string, in CatalogInput) (Object, error) {
	if !textOK(in.Name, 255, true) || !contains([]string{"enabled", "disabled"}, in.Status) || !textOK(in.Reason, 2000, true) || (id != "" && !validID(id)) {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if !groupAdmin(ctx, tx, p) {
		return nil, ErrForbidden
	}
	before := Object{}
	if id == "" {
		err = tx.QueryRow(ctx, `INSERT INTO vendor_catalog(name,status,created_by) VALUES(btrim($1),$2,$3::uuid) RETURNING id::text`, in.Name, in.Status, p.UserID).Scan(&id)
	} else {
		before, err = readObject(ctx, tx, `SELECT name,status FROM vendor_catalog WHERE id=$1::uuid`, id)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE vendor_catalog SET name=btrim($2),status=$3 WHERE id=$1::uuid`, id, in.Name, in.Status)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE vendors SET name=btrim($2) WHERE catalog_id=$1::uuid`, id, in.Name)
		}
	}
	if err != nil {
		return nil, dbError(err)
	}
	if err = refreshInvalidOrders(ctx, tx); err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, p, "", id, "catalog.save", Object{"before": before, "after": Object{"name": in.Name, "status": in.Status}, "reason": in.Reason}); err != nil {
		return nil, err
	}
	return Object{"id": id}, tx.Commit(ctx)
}
func (s *Service) Memberships(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Object], error) {
	if !groupAdmin(ctx, s.pool, p) {
		return pagination.Result[Object]{}, ErrForbidden
	}
	return pageObjects(ctx, s.pool, `SELECT m.vendor_id,m.user_id,c.name vendor_name,u.display_name,m.status,m.valid_until FROM vendor_memberships m JOIN vendor_catalog c ON c.id=m.vendor_id JOIN users u ON u.id=m.user_id ORDER BY c.name,u.display_name,m.user_id`, page)
}
func (s *Service) UpdateMembership(ctx context.Context, p auth.Principal, vendor, user, status, reason string, until time.Time) error {
	if !validID(vendor) || !validID(user) || !contains([]string{"enabled", "disabled"}, status) || !textOK(reason, 2000, true) || (status == "enabled" && !until.After(time.Now())) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !groupAdmin(ctx, tx, p) {
		return ErrForbidden
	}
	before, err := readObject(ctx, tx, `SELECT status,valid_until FROM vendor_memberships WHERE vendor_id=$1::uuid AND user_id=$2::uuid`, vendor, user)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE vendor_memberships SET status=$3,valid_until=$4 WHERE vendor_id=$1::uuid AND user_id=$2::uuid`, vendor, user, status, until); err != nil {
		return err
	}
	if err = refreshInvalidOrders(ctx, tx); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, "", vendor, "membership.update", Object{"user_id": user, "before": before, "after": Object{"status": status, "valid_until": until}, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) Settings(ctx context.Context, p auth.Principal) (Object, error) {
	if !groupAdmin(ctx, s.pool, p) {
		return nil, ErrForbidden
	}
	return readObject(ctx, s.pool, `SELECT overdue_reminder_days FROM remediation_settings WHERE singleton`)
}
func (s *Service) SetSettings(ctx context.Context, p auth.Principal, days int, reason string) error {
	if days < 1 || days > 30 || !textOK(reason, 2000, true) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !groupAdmin(ctx, tx, p) {
		return ErrForbidden
	}
	before, err := readObject(ctx, tx, `SELECT overdue_reminder_days FROM remediation_settings WHERE singleton`)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_settings SET overdue_reminder_days=$1 WHERE singleton`, days); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, "", "settings", "settings.update", Object{"before": before, "after": Object{"overdue_reminder_days": days}, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) TaskSystems(ctx context.Context, p auth.Principal, mine bool, page pagination.Query) (pagination.Result[Object], error) {
	sql, args, err := taskQuery(p, Filter{Mine: mine, Status: "all"})
	if err != nil {
		return pagination.Result[Object]{}, err
	}
	return pageObjects(ctx, s.pool, `SELECT DISTINCT business_system_id id,system_name,organization_id,organization_name FROM (`+sql+`) visible ORDER BY system_name,id`, page, args...)
}
