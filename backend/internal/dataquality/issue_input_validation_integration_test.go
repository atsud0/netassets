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

func TestIssueFieldValidationPreservesIssueEventsAndAudit(t *testing.T) {
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
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE user_id=NULLIF($1,'')::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_issues WHERE id=NULLIF($1,'')::uuid`, issueID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_rules WHERE id=NULLIF($1,'')::uuid`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_scan_batches WHERE id=NULLIF($1,'')::uuid`, batchID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=NULLIF($1,'')::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=NULLIF($1,'')::uuid`, orgID)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'质量校验组织','subsidiary') RETURNING id::text`, "Q_INPUT_"+stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','质量字段测试') RETURNING id::text`, "quality_input_"+stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_organizations(user_id,organization_id,is_primary) VALUES($1::uuid,$2::uuid,true)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_scan_batches(organization_id) VALUES($1::uuid) RETURNING id::text`, orgID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,resource_type,rule_type,target_field,severity) VALUES('字段校验规则','asset','required',$1,'warning') RETURNING id::text`, "custom:input_"+stamp).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_issues(rule_id,resource_type,resource_id,resource_name,organization_id,organization_code,organization_name,rule_name,target_field,severity,first_detected_batch_id,last_detected_batch_id) SELECT id,resource_type,gen_random_uuid(),'校验资源',$1::uuid,'TEST','测试组织',rule_name,target_field,severity,$2::uuid,$2::uuid FROM data_quality_rules WHERE id=$3::uuid RETURNING id::text`, orgID, batchID, ruleID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	s := NewService(pool)
	p := auth.Principal{UserID: userID, Roles: []string{"group_admin"}}
	snapshot := func() string {
		t.Helper()
		var raw string
		if err := pool.QueryRow(ctx, `SELECT json_build_object('issue',(SELECT row_to_json(i) FROM data_quality_issues i WHERE id=$1::uuid),'events',(SELECT json_agg(e ORDER BY id) FROM data_quality_issue_events e WHERE issue_id=$1::uuid),'audits',(SELECT json_agg(a ORDER BY id) FROM audit_logs a WHERE user_id=$2::uuid))::text`, issueID, userID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	for _, tc := range []struct {
		name, message string
		run           func() (Issue, error)
	}{
		{"blank_submit", "处理说明不能为空", func() (Issue, error) { return s.SubmitIssue(ctx, p, issueID, SubmitInput{Note: "　 "}, "", "") }},
		{"oversized_submit", "处理说明最多 5000 字", func() (Issue, error) {
			return s.SubmitIssue(ctx, p, issueID, SubmitInput{Note: strings.Repeat("整", 5001)}, "", "")
		}},
		{"missing_assignee", "请选择有效的整改负责人", func() (Issue, error) { return s.AssignIssue(ctx, p, issueID, StartInput{}, "", "") }},
		{"unavailable_assignee", "整改负责人须为该组织下的启用用户", func() (Issue, error) {
			return s.AssignIssue(ctx, p, issueID, StartInput{AssigneeUserID: "00000000-0000-0000-0000-000000000001"}, "", "")
		}},
		{"blank_return", "退回整改时处理说明不能为空", func() (Issue, error) { return s.ReviewIssue(ctx, p, issueID, ReviewInput{Note: "　 "}, "", "") }},
		{"blank_exemption", "豁免原因不能为空", func() (Issue, error) { return s.ExemptIssue(ctx, p, issueID, ExemptInput{Reason: "　 "}, "", "") }},
		{"bad_date", "豁免有效期格式不正确", func() (Issue, error) {
			return s.ExemptIssue(ctx, p, issueID, ExemptInput{Reason: "原因", ExpiresAt: "bad"}, "", "")
		}},
		{"past_date", "豁免有效期必须晚于当前时间", func() (Issue, error) {
			return s.ExemptIssue(ctx, p, issueID, ExemptInput{Reason: "原因", ExpiresAt: "2020-01-01T00:00:00Z"}, "", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.run()
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("got %v; want %s", err, tc.message)
			}
			if snapshot() != before {
				t.Fatal("rejected action changed issue, events or audit")
			}
		})
	}
	for _, input := range []BatchIssueInput{
		{Action: "submit", Note: "说明"},
		{IDs: strings.Fields(strings.Repeat(issueID+" ", 201)), Action: "submit", Note: "说明"},
		{IDs: []string{issueID, "bad"}, Action: "submit", Note: "说明"},
		{IDs: []string{issueID}, Action: "review"},
		{IDs: []string{issueID}, Action: "assign"},
		{IDs: []string{issueID}, Action: "start", AssigneeUserID: "bad"},
		{IDs: []string{issueID}, Action: "submit", Note: "　 "},
		{IDs: []string{issueID}, Action: "submit", Note: strings.Repeat("😀", 5001)},
	} {
		if _, err := s.OperateIssuesBatch(ctx, p, input, "", ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid batch: %v", err)
		}
		if snapshot() != before {
			t.Fatal("rejected batch changed issue, events or audit")
		}
	}
	// Preserve trim/deduplication and optional notes through a real transaction.
	assigned, err := s.OperateIssuesBatch(ctx, p, BatchIssueInput{IDs: []string{" " + issueID + " ", issueID}, Action: " ASSIGN ", AssigneeUserID: " " + userID + " "}, "", "")
	if err != nil || assigned.TotalCount != 1 || assigned.SuccessCount != 1 || assigned.FailureCount != 0 || len(assigned.Items) != 1 || assigned.Items[0].Item == nil || assigned.Items[0].Item.AssigneeUserID != userID {
		t.Fatalf("normalized batch assignment failed: %+v, %v", assigned, err)
	}
	for _, char := range []string{"中", "N", "😀"} {
		note := strings.Repeat(char, 5000)
		if _, err := s.StartIssue(ctx, p, issueID, StartInput{AssigneeUserID: userID, Note: note}, "", ""); err != nil {
			t.Fatal(err)
		}
		submitted, err := s.SubmitIssue(ctx, p, issueID, SubmitInput{Note: note}, "", "")
		if err != nil || submitted.RectificationNote != note {
			t.Fatalf("submit boundary: %v", err)
		}
		approved, err := s.ReviewIssue(ctx, p, issueID, ReviewInput{Approved: true}, "", "")
		if err != nil || approved.Status != "closed" {
			t.Fatalf("optional review note: %v", err)
		}
		if _, err := s.ReopenIssue(ctx, p, issueID, note, "", ""); err != nil {
			t.Fatal(err)
		}
		exempted, err := s.ExemptIssue(ctx, p, issueID, ExemptInput{Reason: note}, "", "")
		if err != nil || exempted.ExemptionReason != note || exempted.ExemptionExpiresAt != nil {
			t.Fatalf("boundary reason / indefinite exemption: %v", err)
		}
		if _, err := s.ReopenIssue(ctx, p, issueID, "", "", ""); err != nil {
			t.Fatal(err)
		}
		var fullNotes int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_issue_events WHERE issue_id=$1::uuid AND note=$2`, issueID, note).Scan(&fullNotes); err != nil {
			t.Fatal(err)
		}
		if fullNotes != 4 {
			t.Fatalf("full-length %q notes persisted in %d events; want 4", char, fullNotes)
		}
	}
}
