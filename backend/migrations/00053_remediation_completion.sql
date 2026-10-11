-- +goose Up
CREATE TABLE vendor_catalog (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, status text NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled','disabled')),
 created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vendor_catalog_name_idx ON vendor_catalog(lower(btrim(name)));
INSERT INTO vendor_catalog(name,created_by) SELECT DISTINCT ON (lower(btrim(name))) btrim(name),created_by FROM vendors ORDER BY lower(btrim(name)),created_at;
ALTER TABLE vendors ADD COLUMN catalog_id uuid REFERENCES vendor_catalog(id);
UPDATE vendors v SET catalog_id=c.id FROM vendor_catalog c WHERE lower(btrim(v.name))=lower(c.name);
ALTER TABLE vendors ALTER COLUMN catalog_id SET NOT NULL;
CREATE TABLE vendor_memberships (
 vendor_id uuid NOT NULL REFERENCES vendor_catalog(id), user_id uuid NOT NULL REFERENCES users(id),
 status text NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled','disabled')),
 valid_from timestamptz NOT NULL DEFAULT now(), valid_until timestamptz NOT NULL,
 PRIMARY KEY(vendor_id,user_id), CHECK(valid_until>valid_from)
);
INSERT INTO vendor_memberships(vendor_id,user_id,valid_from,valid_until)
 SELECT v.catalog_id,g.user_id,min(g.valid_from),max(g.valid_until) FROM remediation_grants g JOIN vendors v ON v.id=g.vendor_id GROUP BY v.catalog_id,g.user_id;

ALTER TABLE remediation_tasks ADD COLUMN expected_completion_at timestamptz, ADD COLUMN blocked_dependency text NOT NULL DEFAULT '', ADD COLUMN blocked_until timestamptz,
 ADD COLUMN material_needs_review boolean NOT NULL DEFAULT false, ADD COLUMN source_revision int NOT NULL DEFAULT 1, ADD COLUMN assignee_snapshot jsonb NOT NULL DEFAULT '{}';
ALTER TABLE remediation_requests ADD COLUMN mitigation text NOT NULL DEFAULT '';
ALTER TABLE remediation_submissions ADD COLUMN no_deployment_reason text NOT NULL DEFAULT '';
ALTER TABLE remediation_reviews ADD COLUMN verified_environment text NOT NULL DEFAULT '', ADD COLUMN verified_at timestamptz;
ALTER TABLE remediation_material_versions ADD COLUMN source_revision int NOT NULL DEFAULT 1, ADD COLUMN target_environment text NOT NULL DEFAULT '', ADD COLUMN deployment_required boolean NOT NULL DEFAULT true;
UPDATE remediation_material_versions m SET target_environment=t.target_environment,deployment_required=t.deployment_required FROM remediation_tasks t WHERE t.id=m.task_id;
CREATE TABLE remediation_material_attachments (
 task_id uuid NOT NULL, material_revision int NOT NULL, attachment_id uuid NOT NULL REFERENCES remediation_attachments(id),
 PRIMARY KEY(task_id,material_revision,attachment_id), FOREIGN KEY(task_id,material_revision) REFERENCES remediation_material_versions(task_id,revision)
);
CREATE TABLE remediation_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), overdue_reminder_days int NOT NULL DEFAULT 1 CHECK(overdue_reminder_days BETWEEN 1 AND 30)
);
INSERT INTO remediation_settings DEFAULT VALUES;

-- +goose StatementBegin
CREATE FUNCTION remediation_identity(actor uuid, task_key uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('actor_name',COALESCE((SELECT display_name FROM users WHERE id=actor),''),
 'assignee_name',u.display_name,'vendor_name',COALESCE(c.name,'内部人员'),'participant_type',g.participant_type)
 FROM remediation_tasks t JOIN users u ON u.id=t.assignee_user_id JOIN remediation_grants g ON g.id=t.grant_id
 LEFT JOIN vendors v ON v.id=g.vendor_id LEFT JOIN vendor_catalog c ON c.id=v.catalog_id WHERE t.id=task_key
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION remediation_snapshot_assignment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT jsonb_build_object('assignee_name',u.display_name,'vendor_name',COALESCE(c.name,'内部人员'),'participant_type',g.participant_type)
 INTO NEW.assignee_snapshot FROM users u JOIN remediation_grants g ON g.id=NEW.grant_id AND g.user_id=u.id
 LEFT JOIN vendors v ON v.id=g.vendor_id LEFT JOIN vendor_catalog c ON c.id=v.catalog_id WHERE u.id=NEW.assignee_user_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER remediation_snapshot_assignment BEFORE INSERT OR UPDATE OF assignee_user_id,grant_id ON remediation_tasks FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_assignment();
UPDATE remediation_tasks SET assignee_user_id=assignee_user_id;
-- +goose StatementBegin
DO $$
DECLARE spec text[]; rowrecord record;
BEGIN
 FOREACH spec SLICE 1 IN ARRAY ARRAY[['remediation_task_events','actor_id'],['remediation_comments','author_id'],['remediation_submissions','author_id'],['remediation_reviews','reviewer_id'],['remediation_attachments','uploaded_by'],['remediation_material_versions','approved_by']]
 LOOP
  EXECUTE format('ALTER TABLE %I ADD COLUMN identity_snapshot jsonb NOT NULL DEFAULT ''{}''',spec[1]);
  IF spec[1]='remediation_reviews' THEN
   EXECUTE 'UPDATE remediation_reviews r SET identity_snapshot=remediation_identity(r.reviewer_id,s.task_id)||''{"snapshot_origin":"migration"}''::jsonb FROM remediation_submissions s WHERE s.id=r.submission_id';
  ELSE
   EXECUTE format('UPDATE %I SET identity_snapshot=remediation_identity(%I,task_id)||''{"snapshot_origin":"migration"}''::jsonb',spec[1],spec[2]);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION remediation_snapshot_record() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor uuid; task_key uuid;
BEGIN
 actor=(to_jsonb(NEW)->>TG_ARGV[0])::uuid;
 IF TG_TABLE_NAME='remediation_reviews' THEN SELECT task_id INTO task_key FROM remediation_submissions WHERE id=NEW.submission_id;
 ELSE task_key=(to_jsonb(NEW)->>'task_id')::uuid; END IF;
 NEW.identity_snapshot=remediation_identity(actor,task_key);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER remediation_snapshot_event BEFORE INSERT ON remediation_task_events FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('actor_id');
CREATE TRIGGER remediation_snapshot_comment BEFORE INSERT ON remediation_comments FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('author_id');
CREATE TRIGGER remediation_snapshot_submission BEFORE INSERT ON remediation_submissions FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('author_id');
CREATE TRIGGER remediation_snapshot_review BEFORE INSERT ON remediation_reviews FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('reviewer_id');
CREATE TRIGGER remediation_snapshot_attachment BEFORE INSERT ON remediation_attachments FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('uploaded_by');
CREATE TRIGGER remediation_snapshot_material BEFORE INSERT ON remediation_material_versions FOR EACH ROW EXECUTE FUNCTION remediation_snapshot_record('approved_by');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_grant_valid(grant_key uuid, actor uuid, org uuid, system_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_grants g
 JOIN users u ON u.id=g.user_id AND u.status='enabled' AND u.deleted_at IS NULL
 JOIN organizations o ON o.id=g.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
 JOIN business_systems b ON b.id=g.business_system_id AND b.owner_organization_id=g.organization_id AND b.deleted_at IS NULL AND b.system_status<>'archived'
 LEFT JOIN vendors v ON v.id=g.vendor_id LEFT JOIN vendor_catalog c ON c.id=v.catalog_id
 WHERE g.id=grant_key AND g.user_id=actor AND g.organization_id=org AND g.business_system_id=system_key AND g.status='enabled' AND g.valid_from<=now() AND g.valid_until>now()
 AND (g.vendor_id IS NULL OR (v.status='enabled' AND v.organization_id=g.organization_id AND c.status='enabled'
 AND EXISTS(SELECT 1 FROM vendor_memberships vm WHERE vm.vendor_id=c.id AND vm.user_id=actor AND vm.status='enabled' AND vm.valid_from<=now() AND vm.valid_until>now()))))
$$;
-- +goose StatementEnd

-- Source edits retain approved snapshots and invalidate an outstanding review.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_source_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.title,OLD.severity,OLD.description,OLD.evidence,OLD.remediation_advice) IS DISTINCT FROM ROW(NEW.title,NEW.severity,NEW.description,NEW.evidence,NEW.remediation_advice) THEN
  UPDATE remediation_submissions s SET lifecycle_status='returned' FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id
   WHERE s.id=t.current_submission_id AND t.finding_id=NEW.id AND t.status IN ('pending_org_review','pending_security_review') AND w.status NOT IN ('closed','cancelled');
  UPDATE remediation_tasks t SET material_needs_review=true,source_revision=t.source_revision+1,version=t.version+1,updated_at=now(),
   status=CASE WHEN t.status IN ('pending_org_review','pending_security_review') THEN 'in_progress' ELSE t.status END,
   return_reason=CASE WHEN t.status IN ('pending_org_review','pending_security_review') THEN '漏洞材料已变化，请等待管理员复核共享材料后重新提交' ELSE t.return_reason END
   FROM security_work_orders w WHERE w.id=t.work_order_id AND t.finding_id=NEW.id AND t.status NOT IN ('completed','cancelled') AND w.status NOT IN ('closed','cancelled');
  UPDATE security_work_orders w SET status='in_progress',updated_at=now() WHERE w.execution_mode='individual' AND w.status='pending_verification'
   AND EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=w.id AND t.finding_id=NEW.id AND t.material_needs_review);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER remediation_source_changed AFTER UPDATE ON security_findings FOR EACH ROW EXECUTE FUNCTION remediation_source_changed();

-- +goose StatementBegin
CREATE FUNCTION remediation_order_counts(order_key uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('unassigned',count(*) FILTER(WHERE t.id IS NULL),'pending_acceptance',count(*) FILTER(WHERE t.status='pending_acceptance'),
 'in_progress',count(*) FILTER(WHERE t.status='in_progress'),'pending_org_review',count(*) FILTER(WHERE t.status='pending_org_review'),
 'pending_security_review',count(*) FILTER(WHERE t.status='pending_security_review'),'verified_fixed',count(*) FILTER(WHERE t.completion_reason='verified_fixed'),
 'risk_accepted',count(*) FILTER(WHERE t.completion_reason='risk_accepted' AND f.status='risk_accepted' AND f.risk_accepted_until>now()),
 'false_positive',count(*) FILTER(WHERE t.completion_reason='false_positive'),
 'risk_expired',count(*) FILTER(WHERE t.completion_reason='risk_accepted' AND (f.status<>'risk_accepted' OR f.risk_accepted_until<=now())))
 FROM security_work_order_findings l JOIN security_findings f ON f.id=l.finding_id
 LEFT JOIN remediation_tasks t ON t.work_order_id=l.work_order_id AND t.finding_id=l.finding_id AND t.status<>'cancelled' WHERE l.work_order_id=order_key
$$;
-- +goose StatementEnd

-- +goose Down
-- Snapshots and global vendor identities cannot be discarded automatically.
-- Restore the paired database/attachment backup when rolling back this release.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Migration 53 preserves historical identity and material records; restore the release backup to roll back'; END $$;
-- +goose StatementEnd
