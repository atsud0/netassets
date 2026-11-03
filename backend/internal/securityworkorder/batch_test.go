package securityworkorder

import (
	"context"
	"errors"
	"testing"

	"netassets/internal/auth"
)

func TestBatchBoundsAndDuplicateIDs(test *testing.T) {
	for _, input := range []BatchInput{
		{}, {Items: make([]BatchItem, 201)},
		{Items: []BatchItem{{ID: "abcdef00-0000-0000-0000-000000000001"}, {ID: "ABCDEF00-0000-0000-0000-000000000001"}}},
	} {
		if !errors.Is(validateBatch(input), ErrInvalid) {
			test.Fatalf("expected invalid batch: %+v", input)
		}
	}
}

func TestBatchPartialResultsAndCancellation(test *testing.T) {
	items := []BatchItem{{ID: "00000000-0000-0000-0000-000000000001"}, {ID: "00000000-0000-0000-0000-000000000002"}, {ID: "invalid"}}
	result := runBatch(context.Background(), items, func(item BatchItem) error {
		if item.ID == items[1].ID {
			return ErrForbidden
		}
		return nil
	})
	if result.Succeeded != 1 || result.Failed != 2 || result.Items[1].Code != "forbidden" || result.Items[2].Code != "invalid_input" {
		test.Fatalf("unexpected batch result: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = runBatch(ctx, items, func(BatchItem) error { test.Fatal("cancelled batch executed"); return nil })
	if result.Failed != len(items) {
		test.Fatalf("cancelled result: %+v", result)
	}
}

func TestBatchRejectsUnauthorizedBeforeDatabaseAccess(test *testing.T) {
	service := &Service{}
	for _, action := range []func(context.Context, auth.Principal, BatchInput, string, string) (BatchResult, error){
		service.BatchCancel, service.BatchIgnoreCandidates, service.BatchUpdateFindingStatus,
	} {
		if _, err := action(context.Background(), auth.Principal{}, BatchInput{}, "", ""); !errors.Is(err, ErrForbidden) {
			test.Fatalf("expected forbidden, got %v", err)
		}
	}
}
