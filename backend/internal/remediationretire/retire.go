// Package remediationretire holds the single implementation of remediation
// task retirement on asset and business system soft delete, and the matching
// revival on restore. Both dimensions previously drifted: the asset path grew
// its own inline statement while the system path had no marker at all. Keeping
// one implementation means a reviewer sees identical semantics on both axes.
package remediationretire

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AssetColumn and SystemColumn are the marker columns that record why an open
// task stopped being current work.
const (
	AssetColumn  = "asset_retired_at"
	SystemColumn = "system_retired_at"
)

// Retire marks the open remediation work selected by predicate as retired,
// records one timeline event per task, and returns the affected task IDs.
// The caller owns the transaction and must already hold the shared remediation
// advisory lock (pg_advisory_xact_lock(hashtext('netassets-remediation'))) so no
// assignment can create a task after this statement has taken its snapshot.
// retiredColumn must be one of the constants above and must be the same
// dimension that predicate selects.
func Retire(ctx context.Context, tx pgx.Tx, actorID, retiredColumn, predicate string, value any, action string, details map[string]any) ([]string, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		UPDATE remediation_tasks SET %s = now(), version = version + 1, updated_at = now()
		WHERE %s AND status NOT IN ('completed', 'cancelled') AND %s IS NULL
		RETURNING id::text`, retiredColumn, predicate, retiredColumn), value)
	if err != nil {
		return nil, fmt.Errorf("retire remediation tasks: %w", err)
	}
	ids := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan retired remediation task: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retired remediation tasks: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO remediation_task_events(task_id, actor_id, action, visibility, details)
			VALUES($1::uuid, $2::uuid, $3, 'participants', $4::jsonb)
		`, id, actorID, action, details); err != nil {
			return nil, fmt.Errorf("record remediation task retirement event: %w", err)
		}
	}
	return ids, nil
}

// Revive clears the retirement marker on the tasks selected by predicate and
// returns the affected task IDs. A cancelled task is never revived: restoring
// an asset or system must not silently reopen work that was already settled.
func Revive(ctx context.Context, tx pgx.Tx, retiredColumn, predicate string, value any) ([]string, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		UPDATE remediation_tasks SET %s = NULL, version = version + 1, updated_at = now()
		WHERE %s AND %s IS NOT NULL AND status <> 'cancelled'
		RETURNING id::text`, retiredColumn, predicate, retiredColumn), value)
	if err != nil {
		return nil, fmt.Errorf("revive remediation tasks: %w", err)
	}
	ids := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan revived remediation task: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate revived remediation tasks: %w", err)
	}
	return ids, nil
}

// RecordEvents writes one timeline event per task ID. Restore uses it because
// Revive returns IDs but intentionally does not write events: the caller knows
// the restore reason wording for its dimension.
func RecordEvents(ctx context.Context, tx pgx.Tx, actorID, action string, taskIDs []string, details map[string]any) error {
	for _, id := range taskIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO remediation_task_events(task_id, actor_id, action, visibility, details)
			VALUES($1::uuid, $2::uuid, $3, 'participants', $4::jsonb)
		`, id, actorID, action, details); err != nil {
			return fmt.Errorf("record remediation task %s event: %w", action, err)
		}
	}
	return nil
}
