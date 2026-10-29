package dataquality

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestIssueActionsRejectInvalidFieldsBeforeDatabase(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	ctx := context.Background()
	svc := NewService(nil)
	for _, role := range []string{"group_admin", "quality_operator", "asset_operator"} {
		p := auth.Principal{UserID: id, Roles: []string{role}}
		if role == "quality_operator" {
			p.Permissions = []string{qualityPermission}
		}
		for _, tc := range []struct {
			name, message string
			groupOnly     bool
			run           func() (Issue, error)
		}{
			{"start_assignee", "请选择有效的整改负责人", false, func() (Issue, error) { return svc.StartIssue(ctx, p, id, StartInput{AssigneeUserID: "bad"}, "", "") }},
			{"assign_assignee", "请选择有效的整改负责人", false, func() (Issue, error) { return svc.AssignIssue(ctx, p, id, StartInput{}, "", "") }},
			{"submit_blank", "处理说明不能为空", false, func() (Issue, error) { return svc.SubmitIssue(ctx, p, id, SubmitInput{Note: "　 "}, "", "") }},
			{"review_blank", "退回整改时处理说明不能为空", true, func() (Issue, error) { return svc.ReviewIssue(ctx, p, id, ReviewInput{Note: "　 "}, "", "") }},
			{"exempt_blank", "豁免原因不能为空", true, func() (Issue, error) { return svc.ExemptIssue(ctx, p, id, ExemptInput{Reason: "　 "}, "", "") }},
			{"exempt_date", "豁免有效期格式不正确", true, func() (Issue, error) {
				return svc.ExemptIssue(ctx, p, id, ExemptInput{Reason: "理由", ExpiresAt: "bad"}, "", "")
			}},
			{"exempt_past", "豁免有效期必须晚于当前时间", true, func() (Issue, error) {
				return svc.ExemptIssue(ctx, p, id, ExemptInput{Reason: "理由", ExpiresAt: "2020-01-01T00:00:00Z"}, "", "")
			}},
			{"issue_id", "数据质量问题标识格式不正确", false, func() (Issue, error) {
				return svc.SubmitIssue(ctx, p, "bad", SubmitInput{Note: "合法说明"}, "", "")
			}},
		} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				_, err := tc.run()
				if role == "asset_operator" || (tc.groupOnly && role != "group_admin") {
					if !errors.Is(err, ErrForbidden) {
						t.Fatalf("want forbidden, got %v", err)
					}
					return
				}
				if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
					t.Fatalf("want %q, got %v", tc.message, err)
				}
			})
		}
	}
	for _, char := range []string{"中", "N", "😀"} {
		p := auth.Principal{UserID: id, Roles: []string{"group_admin"}}
		note := strings.Repeat(char, 5001)
		for _, run := range []func() (Issue, error){
			func() (Issue, error) { return svc.StartIssue(ctx, p, id, StartInput{Note: note}, "", "") },
			func() (Issue, error) {
				return svc.AssignIssue(ctx, p, id, StartInput{AssigneeUserID: id, Note: note}, "", "")
			},
			func() (Issue, error) { return svc.SubmitIssue(ctx, p, id, SubmitInput{Note: note}, "", "") },
			func() (Issue, error) {
				return svc.ReviewIssue(ctx, p, id, ReviewInput{Approved: true, Note: note}, "", "")
			},
			func() (Issue, error) { return svc.ReopenIssue(ctx, p, id, note, "", "") },
		} {
			if _, err := run(); !errors.Is(err, ErrInvalid) || err.Error() != "处理说明最多 5000 字" {
				t.Fatalf("oversized %q: %v", char, err)
			}
		}
		if _, err := svc.ExemptIssue(ctx, p, id, ExemptInput{Reason: note}, "", ""); !errors.Is(err, ErrInvalid) || err.Error() != "豁免原因最多 5000 字" {
			t.Fatalf("oversized exemption %q: %v", char, err)
		}
	}
}

func TestIssueBatchFieldErrorPreservesSafeMessage(t *testing.T) {
	for _, tc := range []struct {
		err           error
		code, message string
	}{
		{invalidIssue("整改负责人须为该组织下的启用用户"), "invalid", "整改负责人须为该组织下的启用用户"},
		{fmt.Errorf("wrapped: %w", invalidIssue("处理说明最多 5000 字")), "invalid", "处理说明最多 5000 字"},
		{ErrInvalid, "invalid", "整改负责人或处理说明不符合要求"},
		{errors.New("private database detail"), "internal_error", "指派失败，请稍后重试"},
	} {
		code, message := batchIssueError(tc.err, BatchIssueActionAssign)
		if code != tc.code || message != tc.message {
			t.Fatalf("got %s %s", code, message)
		}
	}
}
