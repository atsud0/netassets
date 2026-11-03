-- +goose Up
ALTER TABLE security_work_order_findings
 ADD COLUMN stage text NOT NULL DEFAULT 'not_started' CHECK(stage IN ('not_started','in_progress','pending_verification','verified','returned','disposition')),
 ADD COLUMN version bigint NOT NULL DEFAULT 1,
 ADD COLUMN processing_note text NOT NULL DEFAULT '',
 ADD COLUMN evidence_ids uuid[] NOT NULL DEFAULT '{}',
 ADD COLUMN processed_at timestamptz,
 ADD COLUMN processed_by uuid REFERENCES users(id);

CREATE TABLE security_work_order_item_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_order_id uuid NOT NULL,
 finding_id uuid NOT NULL,
 version bigint NOT NULL,
 action text NOT NULL,
 from_stage text NOT NULL,
 to_stage text NOT NULL,
 note text NOT NULL,
 evidence_ids uuid[] NOT NULL DEFAULT '{}',
 actor_id uuid REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(work_order_id,finding_id) REFERENCES security_work_order_findings(work_order_id,finding_id),
 UNIQUE(work_order_id,finding_id,version)
);

-- +goose StatementBegin
CREATE FUNCTION organization_order_counts(order_key uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('total',count(*),'not_started',count(*) FILTER(WHERE stage='not_started'),
 'in_progress',count(*) FILTER(WHERE stage='in_progress'),'pending_verification',count(*) FILTER(WHERE stage='pending_verification'),
 'verified',count(*) FILTER(WHERE stage='verified'),'returned',count(*) FILTER(WHERE stage='returned'),
 'disposition',count(*) FILTER(WHERE stage='disposition'))
 FROM security_work_order_findings WHERE work_order_id=order_key
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION sync_organization_order(order_key uuid, actor uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
 prior_status text;
 next_status text;
 counts jsonb;
 organization_key uuid;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtext('netassets-remediation'));
 SELECT status,assignee_organization_id INTO prior_status,organization_key FROM security_work_orders
 WHERE id=order_key AND execution_mode='organization' FOR UPDATE;
 IF NOT FOUND OR prior_status IN ('draft','issued','closed','cancelled') THEN RETURN; END IF;
 counts := organization_order_counts(order_key);
 IF (counts->>'total')::int=0 THEN RETURN; END IF;
 next_status := CASE
 WHEN (counts->>'returned')::int>0 THEN 'returned'
 WHEN (counts->>'verified')::int+(counts->>'disposition')::int=(counts->>'total')::int THEN 'closed'
 WHEN (counts->>'pending_verification')::int+(counts->>'verified')::int+(counts->>'disposition')::int=(counts->>'total')::int THEN 'pending_verification'
 ELSE 'in_progress' END;
 UPDATE security_work_orders SET status=next_status,updated_at=clock_timestamp(),updated_by=actor,
 submitted_at=CASE WHEN next_status='pending_verification' THEN COALESCE(submitted_at,now()) ELSE submitted_at END,
 closed_at=CASE WHEN next_status='closed' THEN now() ELSE closed_at END,
 verified_at=CASE WHEN next_status='closed' THEN now() ELSE verified_at END,
 verification_result=CASE WHEN next_status='closed' THEN '关联项目已全部复测通过或经授权处置' ELSE verification_result END
 WHERE id=order_key;
 IF next_status IS DISTINCT FROM prior_status THEN
  INSERT INTO security_work_order_events(work_order_id,actor_user_id,action,from_status,to_status,comment,details)
  VALUES(order_key,actor,'items_aggregate',prior_status,next_status,'按关联项目自动汇总',counts);
  INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details)
  VALUES(actor,organization_key,'security_work_order.items_aggregate','security_work_order',order_key,
  '按关联项目自动汇总',jsonb_build_object('from_status',prior_status,'to_status',next_status,'counts',counts));
 END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION sync_organization_finding_disposition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 linked record;
 next_stage text;
BEGIN
 IF NEW.status IS NOT DISTINCT FROM OLD.status OR current_setting('netassets.order_item_write',true)='on' THEN RETURN NEW; END IF;
 FOR linked IN SELECT link.* FROM security_work_order_findings link JOIN security_work_orders wo ON wo.id=link.work_order_id
 WHERE link.finding_id=NEW.id AND wo.execution_mode='organization' AND wo.status NOT IN ('draft','issued','closed','cancelled')
 ORDER BY link.work_order_id FOR UPDATE OF link LOOP
  IF NEW.status IN ('false_positive','duplicate') OR (NEW.status='risk_accepted' AND NEW.risk_accepted_until>now()) THEN
   next_stage := 'disposition';
  ELSIF linked.stage='disposition' THEN next_stage := 'not_started';
  ELSE CONTINUE; END IF;
  IF next_stage=linked.stage THEN CONTINUE; END IF;
  UPDATE security_work_order_findings SET stage=next_stage,version=version+1,processing_note='安全发现专项处置状态同步',
   processed_by=NEW.updated_by,processed_at=now() WHERE work_order_id=linked.work_order_id AND finding_id=NEW.id;
  INSERT INTO security_work_order_item_history(work_order_id,finding_id,version,action,from_stage,to_stage,note,actor_id)
  VALUES(linked.work_order_id,NEW.id,linked.version+1,'disposition_sync',linked.stage,next_stage,'安全发现专项处置状态同步',NEW.updated_by);
  INSERT INTO security_work_order_events(work_order_id,actor_user_id,action,from_status,to_status,comment,details)
  VALUES(linked.work_order_id,NEW.updated_by,'item_disposition_sync',linked.stage,next_stage,'安全发现专项处置状态同步',jsonb_build_object('finding_id',NEW.id));
  INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details)
  VALUES(NEW.updated_by,NEW.assignee_organization_id,'security_work_order.item_disposition_sync','security_work_order',linked.work_order_id,
   '安全发现专项处置状态同步',jsonb_build_object('finding_id',NEW.id,'from_stage',linked.stage,'to_stage',next_stage,'version',linked.version+1));
  PERFORM sync_organization_order(linked.work_order_id,NEW.updated_by);
 END LOOP;
 RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER organization_finding_disposition AFTER UPDATE OF status ON security_findings
 FOR EACH ROW EXECUTE FUNCTION sync_organization_finding_disposition();

-- +goose Down
DROP TRIGGER organization_finding_disposition ON security_findings;
DROP FUNCTION sync_organization_finding_disposition();
DROP FUNCTION sync_organization_order(uuid,uuid);
DROP FUNCTION organization_order_counts(uuid);
DROP TABLE security_work_order_item_history;
ALTER TABLE security_work_order_findings DROP COLUMN stage,DROP COLUMN version,DROP COLUMN processing_note,
 DROP COLUMN evidence_ids,DROP COLUMN processed_at,DROP COLUMN processed_by;
