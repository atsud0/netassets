-- +goose Up
-- Restoring an asset or a business system clears the retirement marker and the
-- task becomes current work again. Nothing, however, tells the group that this
-- happened: the task simply reappears in the assignee's list, and if the reason it
-- was retired is still unresolved (an archived system, an authorization that
-- lapsed while the asset was gone) it is not even executable. Without a queue,
-- revived work is silently lost between the recycle bin and the executor.
--
-- This function is the single definition of "came back and needs a decision":
-- the most recent lifecycle event on the task must be a restore, and the task must
-- still be open. It reads only the latest lifecycle event, so a later progress
-- event (accept, submit, complete) naturally moves the task out of the queue
-- instead of pinning it there forever.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_recently_revived(task_key uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM remediation_tasks t
   WHERE t.id = task_key
     AND t.status NOT IN ('completed', 'cancelled')
     AND t.updated_at > now() - interval '30 days'
     AND (SELECT e.action FROM remediation_task_events e
          WHERE e.task_id = t.id
          ORDER BY e.created_at DESC, e.id DESC LIMIT 1) IN ('asset_restored', 'system_restored')
 )
$$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS remediation_task_events_recent_idx
    ON remediation_task_events(task_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS remediation_task_events_recent_idx;
DROP FUNCTION IF EXISTS remediation_task_recently_revived(uuid);
