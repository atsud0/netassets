package securitytestbatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestTransitionCommentBoundaries(t *testing.T) {
	for _, action := range []string{"authorize", "start", "complete", "cancel"} {
		for _, char := range []string{"测", "T", "😀"} {
			t.Run(action+"/"+char, func(t *testing.T) {
				if err := validateTransitionComment(action, strings.Repeat(char, 2000)); err != nil {
					t.Fatalf("2000 characters rejected: %v", err)
				}
				err := validateTransitionComment(action, strings.Repeat(char, 2001))
				label := "操作说明"
				if action == "cancel" {
					label = "取消原因"
				}
				assertCommentValidation(t, err, label+"最多 2000 字")
			})
		}
		for _, comment := range []string{"", " \t\n　"} {
			err := validateTransitionComment(action, comment)
			if action == "cancel" {
				assertCommentValidation(t, err, "取消原因不能为空")
			} else if err != nil {
				t.Fatalf("optional %s comment rejected: %v", action, err)
			}
		}
	}
	if err := validateTransitionComment("cancel", " \t"+strings.Repeat("测", 2000)+"　\n"); err != nil {
		t.Fatalf("cancellation trim-before-count compatibility changed: %v", err)
	}
}

func assertCommentValidation(t *testing.T, err error, message string) {
	t.Helper()
	var fieldError *ValidationError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &fieldError) || fieldError.Message != message {
		t.Fatalf("got %v, want ErrInvalid with %q", err, message)
	}
}

func TestTransitionRejectsUnauthorizedBeforeCommentValidation(t *testing.T) {
	service := NewService(nil)
	for _, comment := range []string{"", "   ", strings.Repeat("测", 2001)} {
		for _, transition := range []func(context.Context, auth.Principal, string, TransitionInput, string, string) (Batch, error){
			service.Authorize, service.Start, service.Complete, service.Cancel,
		} {
			_, err := transition(context.Background(), auth.Principal{}, "11111111-1111-4111-8111-111111111111", TransitionInput{Comment: comment}, "", "")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("unauthorized transition returned %v, want ErrForbidden", err)
			}
		}
	}
}
