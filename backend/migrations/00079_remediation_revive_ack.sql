-- +goose Up
-- Migration 00078 derived "this task came back and still needs a decision" from
-- the latest timeline event. That is a guess, not a fact: any later event -
-- including a comment by the manager while they were looking at the task -
-- silently dropped the task out of the queue, and nothing recorded that a human
-- ever made the decision. Reviving is a lifecycle change with an owner and a
-- due answer; it needs its own state.
--
-- revived_at is written when the retirement marker is cleared. revived_ack_at is
-- written only when a manager explicitly confirms the follow-up. The queue is
-- exactly "revived and not yet acknowledged", so it survives unrelated activity
-- and can never loop forever.
ALTER TABLE remediation_tasks
    ADD COLUMN IF NOT EXISTS revived_at timestamptz,
    ADD COLUMN IF NOT EXISTS revived_ack_at timestamptz,
    ADD COLUMN IF NOT EXISTS revived_ack_by uuid REFERENCES users(id);

CREATE INDEX IF NOT EXISTS remediation_tasks_revived_pending_idx
    ON remediation_tasks(updated_at DESC) WHERE revived_at IS NOT NULL AND revived_ack_at IS NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_recently_revived(task_key uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM remediation_tasks t
   WHERE t.id = task_key
     AND t.status NOT IN ('completed', 'cancelled')
     AND t.revived_at IS NOT NULL
     AND t.revived_ack_at IS NULL
 )
$$;
-- +goose StatementEnd

-- Tasks revived under the old event-based rule must not be stranded: adopt the
-- restores already on the timeline as the revival fact, minus the ones that were
-- already progressed afterwards.
UPDATE remediation_tasks t SET revived_at = e.created_at
FROM (
  SELECT DISTINCT ON (task_id) task_id, created_at, id, action
  FROM remediation_task_events
  WHERE action IN ('asset_restored', 'system_restored')
  ORDER BY task_id, created_at DESC, id DESC
) e
WHERE e.task_id = t.id
  AND t.revived_at IS NULL
  AND t.status NOT IN ('completed', 'cancelled')
  AND NOT EXISTS (
    SELECT 1 FROM remediation_task_events later
    WHERE later.task_id = t.id
      AND later.action NOT IN ('asset_restored', 'system_restored')
      AND (later.created_at, later.id) > (e.created_at, e.id)
  );

-- +goose Down
DROP INDEX IF EXISTS remediation_tasks_revived_pending_idx;
DROP FUNCTION IF EXISTS remediation_task_recently_revived(uuid);
ALTER TABLE remediation_tasks
    DROP COLUMN IF EXISTS revived_ack_by,
    DROP COLUMN IF EXISTS revived_ack_at,
    DROP COLUMN IF EXISTS revived_at;
