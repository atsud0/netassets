package inventorytask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestInventoryProgressInputMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      ReviewInput
		message string
	}{
		{"decision", ReviewInput{Decision: "other"}, "请选择有效的审核结果"},
		{"blank reason", ReviewInput{Decision: "returned", ReturnReason: "　 "}, "退回原因不能为空"},
		{"long reason", ReviewInput{Decision: "returned", ReturnReason: strings.Repeat("退", 2001)}, "退回原因最多 2000 字"},
		{"many comments", ReviewInput{Decision: "returned", ReturnReason: "整改", Comments: make([]ReviewCommentInput, 101)}, "逐条审核意见最多填写 100 条"},
		{"resource type", reviewWithComment("other", "resource", "意见"), "请选择有效的审核意见对象类型"},
		{"resource id", reviewWithComment("network_asset", "　 ", "意见"), "请选择审核意见对应的资源"},
		{"blank comment", reviewWithComment("network_asset", "resource", "　 "), "逐条审核意见不能为空"},
		{"long comment", reviewWithComment("network_asset", "resource", strings.Repeat("R", 2001)), "逐条审核意见最多 2000 字"},
		{"duplicate", ReviewInput{Decision: "returned", ReturnReason: "整改", Comments: []ReviewCommentInput{{ResourceType: "network_asset", ResourceID: "asset", Comment: "意见"}, {ResourceType: " network_asset ", ResourceID: " asset ", Comment: "意见二"}}}, "同一资源不能重复填写审核意见"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := normalizeReviewInput(&tc.in)
			var validation *InputValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
	for _, letter := range []string{"R", "审", "😀"} {
		t.Run("character boundaries/"+letter, func(t *testing.T) {
			input := reviewWithComment("network_asset", "resource", "　"+strings.Repeat(letter, 2000)+" ")
			input.ReturnReason = " " + strings.Repeat(letter, 2000) + "　"
			if err := normalizeReviewInput(&input); err != nil {
				t.Fatalf("2000 characters: %v", err)
			}
			if input.Comments[0].Comment != strings.Repeat(letter, 2000) || input.ReturnReason != strings.Repeat(letter, 2000) {
				t.Fatal("text was truncated or not trimmed")
			}
			input = reviewWithComment("network_asset", "resource", strings.Repeat(letter, 2001))
			if err := normalizeReviewInput(&input); err == nil || err.Error() != "逐条审核意见最多 2000 字" {
				t.Fatalf("2001 characters: %v", err)
			}
		})
	}
	for _, letter := range []string{"N", "交", "😀"} {
		_, err := NewService(nil).Submit(context.Background(), auth.Principal{Roles: []string{"organization_admin"}}, "task", "org", SubmissionInput{Note: strings.Repeat(letter, 5001)}, "", "")
		if err == nil || err.Error() != "提交说明最多 5000 字" {
			t.Errorf("submission %s: %v", letter, err)
		}
	}
}

func reviewWithComment(kind, id, note string) ReviewInput {
	return ReviewInput{Decision: "returned", ReturnReason: "请整改", Comments: []ReviewCommentInput{{ResourceType: kind, ResourceID: id, Comment: note}}}
}
