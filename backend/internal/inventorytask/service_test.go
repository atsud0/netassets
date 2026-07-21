package inventorytask

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeCreateInput(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name:            "  季度填报  ",
		StartAt:         start,
		DeadlineAt:      start.Add(7 * 24 * time.Hour),
		Status:          "active",
		OrganizationIDs: []string{"org-1", "org-1", " org-2 "},
		RequiredFields:  []string{"network_assets", "network_assets", "business_systems"},
	}
	if err := normalizeCreateInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Name != "季度填报" || len(input.OrganizationIDs) != 2 || len(input.RequiredFields) != 2 {
		t.Fatalf("unexpected normalized input: %+v", input)
	}
}

func TestNormalizeCreateInputRejectsInvalidRange(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{Name: "无效任务", StartAt: start, DeadlineAt: start, OrganizationIDs: []string{"org-1"}}
	if err := normalizeCreateInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeCreateInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeReviewInput(t *testing.T) {
	input := ReviewInput{Decision: " returned ", ReturnReason: " 负责人信息不完整 "}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Decision != "returned" || input.ReturnReason != "负责人信息不完整" {
		t.Fatalf("unexpected normalized review input: %+v", input)
	}
}

func TestNormalizeReviewInputRequiresReturnReason(t *testing.T) {
	input := ReviewInput{Decision: "returned"}
	if err := normalizeReviewInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeReviewInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeReviewInputClearsApprovalReason(t *testing.T) {
	input := ReviewInput{Decision: "approved", ReturnReason: "unused"}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.ReturnReason != "" {
		t.Fatalf("return reason = %q, want empty", input.ReturnReason)
	}
}
