package inventorytask

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

func TestInventoryTaskInputValidationDoesNotMutate(t *testing.T) {
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
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	var userID, orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','填报字段边界测试') RETURNING id::text`, "task_validation_"+stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM inventory_task_organizations WHERE task_id IN (SELECT id FROM inventory_tasks WHERE created_by=$1::uuid)`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM inventory_tasks WHERE created_by=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=NULLIF($1,'')::uuid`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'填报边界组织','subsidiary') RETURNING id::text`, "TASK_VAL_"+stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool)
	p := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}}
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := func() CreateInput {
		return CreateInput{Name: strings.Repeat("填", 255), Description: strings.Repeat("说", 5000), StartAt: start, DeadlineAt: start.Add(time.Hour), Status: "draft", OrganizationIDs: []string{orgID}, RequiredFields: []string{"business_systems", "network_assets"}}
	}
	created, err := svc.Create(ctx, p, valid(), "task-validation", "")
	if err != nil {
		t.Fatal(err)
	}
	var name, description string
	if err := pool.QueryRow(ctx, `SELECT task_name,description FROM inventory_tasks WHERE id=$1::uuid`, created.ID).Scan(&name, &description); err != nil {
		t.Fatal(err)
	}
	if name != valid().Name || description != valid().Description {
		t.Fatal("full length text was not preserved")
	}
	in := valid()
	in.Name = strings.Repeat("T", 255)
	in.Description = strings.Repeat("D", 5000)
	if _, err := svc.Create(ctx, p, in, "task-validation", ""); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var raw string
		err := pool.QueryRow(ctx, `SELECT json_build_object('tasks',(SELECT json_agg(t ORDER BY id) FROM inventory_tasks t WHERE created_by=$1::uuid),'organizations',(SELECT json_agg(o ORDER BY task_id,organization_id) FROM inventory_task_organizations o WHERE task_id IN (SELECT id FROM inventory_tasks WHERE created_by=$1::uuid)),'audits',(SELECT json_agg(a ORDER BY id) FROM audit_logs a WHERE user_id=$1::uuid),'task_count',(SELECT count(*) FROM inventory_tasks),'progress_count',(SELECT count(*) FROM inventory_task_organizations))::text`, userID).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	// Validation of progress input must not change even an existing task or its audits.
	contributor := auth.Principal{UserID: userID, Roles: []string{"organization_admin"}}
	for _, letter := range []string{"N", "交", "😀"} {
		_, err := svc.Submit(ctx, contributor, created.ID, orgID, SubmissionInput{Note: strings.Repeat(letter, 5001)}, "invalid-submit", "")
		if !errors.Is(err, ErrInvalid) || err.Error() != "提交说明最多 5000 字" {
			t.Fatalf("invalid submission: %v", err)
		}
		if snapshot() != before {
			t.Fatal("invalid submission changed tasks, progress or audits")
		}
	}
	for _, input := range []ReviewInput{
		{Decision: "other"},
		{Decision: "returned", ReturnReason: "　 "},
		{Decision: "returned", ReturnReason: strings.Repeat("退", 2001)},
		reviewWithComment("network_asset", "resource", strings.Repeat("审", 2001)),
	} {
		_, err := svc.Review(ctx, p, created.ID, orgID, input, "invalid-review", "")
		var validation *InputValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) {
			t.Fatalf("invalid review: %v", err)
		}
		if snapshot() != before {
			t.Fatal("invalid review changed tasks, progress or audits")
		}
	}
	for _, tc := range []struct {
		name    string
		change  func(*CreateInput)
		message string
	}{
		{"blank", func(in *CreateInput) { in.Name = "　 " }, "任务名称不能为空"},
		{"long_name", func(in *CreateInput) { in.Name = strings.Repeat("填", 256) }, "任务名称最多 255 字"},
		{"long_description", func(in *CreateInput) { in.Description = strings.Repeat("说", 5001) }, "任务说明最多 5000 字"},
		{"time", func(in *CreateInput) { in.DeadlineAt = in.StartAt }, "截止时间必须晚于开始时间"},
		{"scope", func(in *CreateInput) { in.AssetNetworkScope = "bad" }, "请选择有效的网络资产核查范围"},
		{"organizations", func(in *CreateInput) { in.OrganizationIDs = nil }, "请选择至少一个填报组织"},
		{"fields", func(in *CreateInput) { in.RequiredFields = []string{"bad"} }, "请选择有效的填报内容"},
		{"resource", func(in *CreateInput) { in.RequiredFields = []string{"owner_contacts"} }, "请至少选择业务系统、数据安全属性或网络资产中的一项填报内容"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid()
			tc.change(&in)
			_, err := svc.Create(ctx, p, in, "task-validation", "")
			var validation *InputValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != tc.message {
				t.Fatalf("create: %v", err)
			}
			if after := snapshot(); after != before {
				t.Fatal("invalid creation changed tasks, progress or audit")
			}
		})
	}
	for _, tc := range []struct {
		in      ScopePreviewInput
		message string
	}{
		{ScopePreviewInput{OrganizationIDs: []string{orgID}, RequiredFields: []string{"network_assets"}, AssetNetworkScope: "bad"}, "请选择有效的网络资产核查范围"},
		{ScopePreviewInput{OrganizationIDs: nil, RequiredFields: []string{"network_assets"}}, "请选择至少一个填报组织"},
		{ScopePreviewInput{OrganizationIDs: []string{orgID}, RequiredFields: []string{"bad"}}, "请选择有效的填报内容"},
	} {
		_, err := svc.PreviewScope(ctx, p, tc.in)
		if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
			t.Fatalf("preview: %v", err)
		}
		if after := snapshot(); after != before {
			t.Fatal("invalid preview changed tasks, progress or audit")
		}
	}
	preview, err := svc.PreviewScope(ctx, p, ScopePreviewInput{OrganizationIDs: []string{orgID}, RequiredFields: []string{"business_systems", "network_assets"}})
	if err != nil || preview.OrganizationCount != 1 || preview.Items[0].OrganizationID != orgID {
		t.Fatalf("valid preview: %+v %v", preview, err)
	}
	if after := snapshot(); after != before {
		t.Fatal("valid preview changed tasks, progress or audit")
	}
	for _, role := range []string{"organization_admin", "security_operator", "asset_operator", "remediation_executor"} {
		other := auth.Principal{UserID: userID, Roles: []string{role}}
		if _, err := svc.Create(ctx, other, valid(), "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s create: %v", role, err)
		}
		if _, err := svc.PreviewScope(ctx, other, ScopePreviewInput{}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s preview: %v", role, err)
		}
		if after := snapshot(); after != before {
			t.Fatal("forbidden action changed tasks, progress or audit")
		}
	}
	missing := valid()
	missing.OrganizationIDs = []string{"00000000-0000-0000-0000-000000000000"}
	if _, err := svc.Create(ctx, p, missing, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing organization create: %v", err)
	}
	if _, err := svc.PreviewScope(ctx, p, ScopePreviewInput{OrganizationIDs: missing.OrganizationIDs, RequiredFields: missing.RequiredFields}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing organization preview: %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatal("missing organization changed tasks, progress or audit")
	}
	// DB faults retain server-error classification, not a public input message.
	closedPool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	closedPool.Close()
	if _, err := NewService(closedPool).Create(ctx, p, valid(), "", ""); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("create database failure: %v", err)
	}
	if _, err := NewService(closedPool).PreviewScope(ctx, p, ScopePreviewInput{OrganizationIDs: []string{orgID}, RequiredFields: []string{"network_assets"}}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("preview database failure: %v", err)
	}
	// Persist full Unicode comments through the real review transaction, snapshot and audit path.
	if _, err := pool.Exec(ctx, `UPDATE inventory_tasks SET status='active',require_review=true WHERE id=$1::uuid`, created.ID); err != nil {
		t.Fatal(err)
	}
	for version, letter := range []string{"R", "审", "😀"} {
		var submissionID, resourceID string
		if err := pool.QueryRow(ctx, `INSERT INTO inventory_submissions(task_id,organization_id,submission_version,submitted_by) VALUES($1::uuid,$2::uuid,$3,$4::uuid) RETURNING id::text`, created.ID, orgID, version+1, userID).Scan(&submissionID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO inventory_submission_items(submission_id,resource_type,resource_id,resource_key,resource_name) VALUES($1::uuid,'network_asset',gen_random_uuid(),'COMMENT_BOUNDARY','审核意见字符边界') RETURNING resource_id::text`, submissionID).Scan(&resourceID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE inventory_task_organizations SET status='submitted',latest_submission_id=$3::uuid WHERE task_id=$1::uuid AND organization_id=$2::uuid`, created.ID, orgID, submissionID); err != nil {
			t.Fatal(err)
		}
		text := strings.Repeat(letter, 2000)
		input := reviewWithComment("network_asset", resourceID, "　"+text+" ")
		input.ReturnReason = " " + text + "　"
		result, err := svc.Review(ctx, p, created.ID, orgID, input, "review-unicode-boundary", "")
		if err != nil {
			t.Fatalf("persist %s review: %v", letter, err)
		}
		if result.Status != "returned" || result.ReturnReason != text {
			t.Fatal("review result did not preserve full reason")
		}
		var comment, reason, status string
		if err := pool.QueryRow(ctx, `SELECT c.comment,s.return_reason,s.review_status FROM inventory_submission_review_comments c JOIN inventory_submissions s ON s.id=c.submission_id WHERE s.id=$1::uuid`, submissionID).Scan(&comment, &reason, &status); err != nil {
			t.Fatal(err)
		}
		if comment != text || reason != text || status != "returned" {
			t.Fatal("persisted review truncated text or changed status")
		}
		var auditCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid AND resource_id=$2 AND action='inventory_task.review' AND request_id='review-unicode-boundary'`, userID, created.ID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount != version+1 {
			t.Fatalf("review audit count = %d, want %d", auditCount, version+1)
		}
	}

}
