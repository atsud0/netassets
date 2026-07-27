-- +goose Up
CREATE INDEX audit_logs_action_created_idx ON audit_logs(action, created_at DESC);
CREATE INDEX audit_logs_user_created_idx ON audit_logs(user_id, created_at DESC);
CREATE INDEX audit_logs_result_created_idx ON audit_logs(result, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS audit_logs_result_created_idx;
DROP INDEX IF EXISTS audit_logs_user_created_idx;
DROP INDEX IF EXISTS audit_logs_action_created_idx;
