-- +goose Up
INSERT INTO permissions (permission_key, permission_name) VALUES
 ('remediation.assign', '分派修复任务'), ('remediation.review', '确认修复材料'),
 ('remediation.people.manage', '管理修复人员授权'), ('remediation.execute', '执行本人修复任务'),
 ('vendor.manage', '管理合作厂商') ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_key)
 SELECT r.id, p.permission_key FROM roles r CROSS JOIN permissions p
 WHERE r.role_key IN ('group_admin','organization_admin') AND p.permission_key IN
 ('remediation.assign','remediation.review','remediation.people.manage','vendor.manage') ON CONFLICT DO NOTHING;

ALTER TABLE security_work_orders ADD COLUMN execution_mode text NOT NULL DEFAULT 'organization'
 CHECK (execution_mode IN ('organization','individual'));

CREATE TABLE vendors (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(255) NOT NULL,
 organization_id uuid NOT NULL REFERENCES organizations(id),
 contact varchar(255) NOT NULL DEFAULT '', status text NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled','disabled')),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(organization_id, name)
);
CREATE TABLE remediation_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id),
 organization_id uuid NOT NULL REFERENCES organizations(id), business_system_id uuid NOT NULL REFERENCES business_systems(id),
 vendor_id uuid REFERENCES vendors(id), participant_type text NOT NULL CHECK(participant_type IN ('internal','external')),
 status text NOT NULL DEFAULT 'enabled' CHECK(status IN ('enabled','revoked')),
 valid_from timestamptz NOT NULL DEFAULT now(), valid_until timestamptz NOT NULL,
 sponsor_user_id uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(valid_until > valid_from), CHECK((participant_type='external')=(vendor_id IS NOT NULL))
);
CREATE UNIQUE INDEX remediation_grants_active_idx ON remediation_grants(user_id,organization_id,business_system_id) WHERE status='enabled';
CREATE INDEX remediation_grants_scope_idx ON remediation_grants(organization_id,business_system_id,status);
CREATE TABLE remediation_invitations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), token_hash text NOT NULL UNIQUE,
 organization_id uuid NOT NULL REFERENCES organizations(id), business_system_id uuid NOT NULL REFERENCES business_systems(id),
 vendor_id uuid REFERENCES vendors(id), participant_type text NOT NULL CHECK(participant_type IN ('internal','external')),
 intended_username varchar(128) NOT NULL, display_name varchar(128) NOT NULL,
 inviter_id uuid NOT NULL REFERENCES users(id), valid_until timestamptz NOT NULL,
 expires_at timestamptz NOT NULL, accepted_at timestamptz, revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE remediation_tasks (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_no text NOT NULL UNIQUE,
 work_order_id uuid NOT NULL REFERENCES security_work_orders(id), finding_id uuid NOT NULL REFERENCES security_findings(id),
 organization_id uuid NOT NULL REFERENCES organizations(id), business_system_id uuid NOT NULL REFERENCES business_systems(id),
 assignee_user_id uuid NOT NULL REFERENCES users(id), grant_id uuid NOT NULL REFERENCES remediation_grants(id),
 status text NOT NULL DEFAULT 'pending_acceptance' CHECK(status IN ('pending_acceptance','in_progress','pending_org_review','pending_security_review','completed','cancelled')),
 completion_reason text CHECK(completion_reason IN ('verified_fixed','risk_accepted','false_positive')),
 due_at timestamptz NOT NULL, original_due_at timestamptz NOT NULL,
 target_environment text NOT NULL, deployment_required boolean NOT NULL DEFAULT true,
 progress text NOT NULL DEFAULT '', blocked_reason text NOT NULL DEFAULT '', return_reason text NOT NULL DEFAULT '',
 current_submission_id uuid, material_revision int NOT NULL DEFAULT 1, version int NOT NULL DEFAULT 1,
 accepted_at timestamptz, completed_at timestamptz,
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX remediation_tasks_finding_idx ON remediation_tasks(work_order_id,finding_id) WHERE status <> 'cancelled';
CREATE INDEX remediation_tasks_assignee_idx ON remediation_tasks(assignee_user_id,status,due_at);
CREATE INDEX remediation_tasks_org_idx ON remediation_tasks(organization_id,status,due_at);
CREATE TABLE remediation_material_versions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_id uuid NOT NULL REFERENCES remediation_tasks(id), revision int NOT NULL,
 title text NOT NULL, severity text NOT NULL, affected_location text NOT NULL DEFAULT '', description text NOT NULL,
 evidence text NOT NULL DEFAULT '', remediation_advice text NOT NULL DEFAULT '', requirement text NOT NULL,
 approved_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(task_id,revision)
);
CREATE TABLE remediation_submissions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_id uuid NOT NULL REFERENCES remediation_tasks(id), revision int NOT NULL,
 material_revision int NOT NULL, author_id uuid NOT NULL REFERENCES users(id),
 remediation_summary text NOT NULL, deployment_status text NOT NULL CHECK(deployment_status IN ('code_only','test','target','not_applicable')),
 target_environment text NOT NULL, change_reference text NOT NULL DEFAULT '', deployed_at timestamptz,
 self_test_result text NOT NULL, lifecycle_status text NOT NULL DEFAULT 'current' CHECK(lifecycle_status IN ('current','withdrawn','returned','completed','cancelled')),
 submitted_at timestamptz NOT NULL DEFAULT now(), UNIQUE(task_id,revision)
);
ALTER TABLE remediation_tasks ADD CONSTRAINT remediation_tasks_current_submission_fk FOREIGN KEY(current_submission_id) REFERENCES remediation_submissions(id);
CREATE UNIQUE INDEX remediation_current_submission_idx ON remediation_submissions(task_id) WHERE lifecycle_status='current';
CREATE TABLE remediation_reviews (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), submission_id uuid NOT NULL REFERENCES remediation_submissions(id),
 stage text NOT NULL CHECK(stage IN ('organization','security')), decision text NOT NULL CHECK(decision IN ('pass','return')),
 public_feedback text NOT NULL, internal_note text NOT NULL DEFAULT '', reviewer_id uuid NOT NULL REFERENCES users(id),
 reviewed_at timestamptz NOT NULL DEFAULT now(), UNIQUE(submission_id,stage)
);
CREATE TABLE remediation_comments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_id uuid NOT NULL REFERENCES remediation_tasks(id),
 author_id uuid NOT NULL REFERENCES users(id), visibility text NOT NULL CHECK(visibility IN ('participants','internal')),
 body text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE remediation_attachments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_id uuid NOT NULL REFERENCES remediation_tasks(id),
 visibility text NOT NULL CHECK(visibility IN ('participants','internal')), storage_key text NOT NULL UNIQUE,
 original_name text NOT NULL, content_type text NOT NULL, size_bytes bigint NOT NULL CHECK(size_bytes > 0),
 uploaded_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE remediation_submission_attachments (
 submission_id uuid NOT NULL REFERENCES remediation_submissions(id), attachment_id uuid NOT NULL REFERENCES remediation_attachments(id),
 PRIMARY KEY(submission_id,attachment_id)
);
CREATE TABLE remediation_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), task_id uuid NOT NULL REFERENCES remediation_tasks(id), author_id uuid NOT NULL REFERENCES users(id),
 type text NOT NULL CHECK(type IN ('reassign','extension','false_positive','risk_acceptance')),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
 reason text NOT NULL, proposed_due_at timestamptz, decision text NOT NULL DEFAULT '',
 decided_by uuid REFERENCES users(id), decided_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX remediation_pending_request_idx ON remediation_requests(task_id,type) WHERE status='pending';
CREATE TABLE remediation_task_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, task_id uuid NOT NULL REFERENCES remediation_tasks(id),
 actor_id uuid NOT NULL REFERENCES users(id), action text NOT NULL, visibility text NOT NULL DEFAULT 'participants' CHECK(visibility IN ('participants','internal')),
 details jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX remediation_events_task_idx ON remediation_task_events(task_id,id DESC);
CREATE TABLE remediation_idempotency (
 user_id uuid NOT NULL REFERENCES users(id), operation text NOT NULL, key text NOT NULL, fingerprint text NOT NULL,
 result jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,operation,key)
);

-- Permission and organization scope must come from the same enabled role.
-- +goose StatementBegin
CREATE FUNCTION remediation_can_manage(actor uuid, organization uuid, permission text) RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH RECURSIVE scope(id, recursive) AS (
  SELECT ur.organization_id, ur.data_scope='organization_and_descendants'
  FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
  JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key=permission
  JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
  WHERE ur.user_id=actor AND ur.data_scope IN ('organization','organization_and_descendants')
  UNION
  SELECT child.id, true FROM organizations child JOIN scope parent ON child.parent_id=parent.id
  WHERE parent.recursive AND child.status='enabled' AND child.deleted_at IS NULL
 )
 SELECT EXISTS(SELECT 1 FROM users WHERE id=actor AND status='enabled' AND deleted_at IS NULL) AND (
 EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
        WHERE ur.user_id=actor AND r.role_key='group_admin') OR EXISTS(SELECT 1 FROM scope WHERE id=organization))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION remediation_is_security(actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
 JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='security_workorder.manage'
 JOIN users u ON u.id=ur.user_id AND u.status='enabled' AND u.deleted_at IS NULL
 WHERE ur.user_id=actor AND r.role_key IN ('group_admin','security_operator'))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION remediation_grant_valid(grant_key uuid, actor uuid, org uuid, system_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_grants g
 JOIN users u ON u.id=g.user_id AND u.status='enabled' AND u.deleted_at IS NULL
 JOIN organizations o ON o.id=g.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
 JOIN business_systems b ON b.id=g.business_system_id AND b.owner_organization_id=g.organization_id AND b.deleted_at IS NULL AND b.system_status <> 'archived'
 LEFT JOIN vendors v ON v.id=g.vendor_id
 WHERE g.id=grant_key AND g.user_id=actor AND g.organization_id=org AND g.business_system_id=system_key
 AND g.status='enabled' AND g.valid_from<=now() AND g.valid_until>now()
 AND (g.vendor_id IS NULL OR (v.status='enabled' AND v.organization_id=g.organization_id)))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id))))
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION remediation_can_read(uuid,uuid);
DROP FUNCTION remediation_grant_valid(uuid,uuid,uuid,uuid);
DROP FUNCTION remediation_is_security(uuid);
DROP FUNCTION remediation_can_manage(uuid,uuid,text);
DROP TABLE remediation_idempotency, remediation_task_events, remediation_requests, remediation_submission_attachments, remediation_attachments, remediation_comments, remediation_reviews;
ALTER TABLE remediation_tasks DROP CONSTRAINT remediation_tasks_current_submission_fk;
DROP TABLE remediation_submissions, remediation_material_versions, remediation_tasks, remediation_invitations, remediation_grants, vendors;
ALTER TABLE security_work_orders DROP COLUMN execution_mode;
DELETE FROM role_permissions WHERE permission_key IN ('remediation.assign','remediation.review','remediation.people.manage','remediation.execute','vendor.manage');
DELETE FROM permissions WHERE permission_key IN ('remediation.assign','remediation.review','remediation.people.manage','remediation.execute','vendor.manage');
