package dataquality

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

func TestIssueAssignmentAndChineseNotes(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	var orgID, userID, batchID, issueID, ruleID string
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_issues WHERE id=$1::uuid`, issueID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_rules WHERE id=$1::uuid`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_scan_batches WHERE id=$1::uuid`, batchID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, orgID)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'质量回归组织','subsidiary') RETURNING id::text`, "Q_NOTE_"+stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','质量回归用户') RETURNING id::text`, "quality_note_"+stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_organizations(user_id,organization_id,is_primary) VALUES($1::uuid,$2::uuid,true)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_scan_batches(organization_id) VALUES($1::uuid) RETURNING id::text`, orgID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,resource_type,rule_type,target_field,severity) VALUES('说明边界规则','asset','required',$1,'warning') RETURNING id::text`, "custom:note_"+stamp).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_issues(rule_id,resource_type,resource_id,resource_name,organization_id,organization_code,organization_name,rule_name,target_field,severity,first_detected_batch_id,last_detected_batch_id)
	 SELECT id,resource_type,gen_random_uuid(),'测试资源',$1::uuid,'TEST','测试组织',rule_name,target_field,severity,$2::uuid,$2::uuid FROM data_quality_rules WHERE id=$3::uuid RETURNING id::text`, orgID, batchID, ruleID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	s := NewService(pool)
	p := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}}
	note := strings.Repeat("调", 5000)
	assigned, err := s.AssignIssue(ctx, p, issueID, StartInput{AssigneeUserID: userID, Note: note}, "", "")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if assigned.AssigneeUserID != userID || assigned.Status != "pending" {
		t.Fatalf("assigned = %+v", assigned)
	}
	var eventNote string
	if err := pool.QueryRow(ctx, `SELECT note FROM data_quality_issue_events WHERE issue_id=$1::uuid AND event_type='assigned'`, issueID).Scan(&eventNote); err != nil {
		t.Fatal(err)
	}
	if eventNote != note {
		t.Fatal("assignment note was truncated")
	}
	result, err := s.OperateIssuesBatch(ctx, p, BatchIssueInput{IDs: []string{issueID}, Action: "assign", AssigneeUserID: userID, Note: note}, "", "")
	if err != nil || result.SuccessCount != 1 {
		t.Fatalf("batch = %+v, error = %v", result, err)
	}
	if _, err := s.SubmitIssue(ctx, p, issueID, SubmitInput{Note: note}, "", ""); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := s.ExemptIssue(ctx, p, issueID, ExemptInput{Reason: note}, "", ""); err != nil {
		t.Fatalf("exempt: %v", err)
	}
	if _, err := s.ReopenIssue(ctx, p, issueID, note, "", ""); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s.AssignIssue(ctx, p, issueID, StartInput{AssigneeUserID: userID, Note: note + "调"}, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized note: %v", err)
	}
	// Invalid/inactive assignees must not be accepted to make the positive case pass.
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1::uuid`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignIssue(ctx, p, issueID, StartInput{AssigneeUserID: userID}, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled assignee: %v", err)
	}
}
