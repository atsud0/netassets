package remediation_test

import (
	"testing"

	"netassets/internal/inventorytask"
	"netassets/internal/remediation"
)

// A subsidiary never deletes a published business system directly: it files a
// working-version delete and the group approves the publication. That path
// archives the same business_systems row as the group-admin delete, so it must
// retire open remediation work with the identical semantics. Before this test the
// reporting flow archived the system silently and left the task looking current.
func TestWorkingInventorySystemDeleteRetiresOpenRemediationTasks(t *testing.T) {
	f := setup(t)
	tasks := inventorytask.NewService(f.pool)

	id := f.assign(t, 0, "working-delete-retire", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)

	var taskID, orgID string
	must(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO inventory_tasks(task_name,start_at,deadline_at,status,created_by,require_review)
		VALUES('业务系统下线采集任务',now(),now()+interval '7 days','active',$1::uuid,true) RETURNING id::text`, f.group.UserID).Scan(&taskID))
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO inventory_task_organizations(task_id,organization_id,status)
		VALUES($1::uuid,$2::uuid,'submitted')`, taskID, f.org)
	must(t, err)
	var snapshotID string
	must(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO inventory_submissions(task_id,organization_id,submission_version,submitted_by,submission_note,system_count,asset_count)
		VALUES($1::uuid,$2::uuid,1,$3::uuid,'子公司提交业务系统下线工作版本',1,0) RETURNING id::text`, taskID, f.org, f.group.UserID).Scan(&snapshotID))
	_, err = f.pool.Exec(f.ctx, `UPDATE inventory_task_organizations SET latest_submission_id=$3::uuid WHERE task_id=$1::uuid AND organization_id=$2::uuid`, taskID, f.org, snapshotID)
	must(t, err)
	orgID = f.org
	_, err = f.pool.Exec(f.ctx, `
		INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,source_system_id,system_code,system_name,system_status,system_level,contains_personal_information,contains_sensitive_personal_information,data_volume_over_100k,contains_important_data,contains_core_data,draft_status,created_by,updated_by,last_edited_by,version)
		VALUES($1::uuid,$2::uuid,'delete',$3::uuid,$4,$5,'running','normal','unknown','unknown','unknown','unknown','unknown','submitted',$6::uuid,$6::uuid,$6::uuid,1)`,
		taskID, f.org, f.system, "RSYS_WD", "修复测试系统", f.group.UserID)
	must(t, err)

	if _, err := tasks.Review(f.ctx, f.group, taskID, orgID, inventorytask.ReviewInput{Decision: "approved"}, "working-delete-review", "127.0.0.1"); err != nil {
		t.Fatalf("approve working delete: %v", err)
	}

	var deletedAt any
	var systemStatus string
	must(t, f.pool.QueryRow(f.ctx, `SELECT deleted_at, system_status FROM business_systems WHERE id=$1::uuid`, f.system).Scan(&deletedAt, &systemStatus))
	if deletedAt == nil {
		t.Fatal("the approved working delete did not archive the published business system")
	}
	var retirement any
	var taskStatus string
	must(t, f.pool.QueryRow(f.ctx, `SELECT status, system_retired_at FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&taskStatus, &retirement))
	if taskStatus != "in_progress" || retirement == nil {
		t.Fatalf("task after working delete = status %q retired %v; history must survive and be marked retired", taskStatus, retirement)
	}
	var retiredEvents int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_task_events WHERE task_id=$1::uuid AND action='system_retired'`, id).Scan(&retiredEvents))
	if retiredEvents != 1 {
		t.Fatalf("system_retired events = %d, want 1", retiredEvents)
	}
}
