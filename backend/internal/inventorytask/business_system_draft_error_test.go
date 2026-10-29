package inventorytask

import (
	"errors"
	"fmt"
	"testing"

	"netassets/internal/businesssystem"
)

func TestBusinessSystemDraftPreservesOwnerContactError(t *testing.T) {
	for _, err := range []error{businesssystem.ErrInvalidOwnerContact, fmt.Errorf("validate draft: %w", businesssystem.ErrInvalidOwnerContact)} {
		mapped := mapBusinessSystemDraftError(err)
		if !errors.Is(mapped, ErrInvalid) || !errors.Is(mapped, businesssystem.ErrInvalidOwnerContact) {
			t.Fatalf("mapped error must retain both invalid draft and contact identities: %v", mapped)
		}
	}
	if got := mapBusinessSystemDraftError(businesssystem.ErrInvalid); got != ErrInvalid {
		t.Fatalf("generic invalid mapping changed: %v", got)
	}
}
