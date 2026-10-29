package asset

import (
	"errors"
	"strings"
	"testing"
)

func TestDirectChangeReasonCharacterLimit(t *testing.T) {
	for _, reason := range []string{strings.Repeat("原", DirectChangeReasonMax), strings.Repeat("a", DirectChangeReasonMax)} {
		if err := validateDirectChangeReason(reason); err != nil {
			t.Fatalf("valid reason rejected: %v", err)
		}
		if err := validateDirectChangeReason(reason + "因"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("overlong reason accepted: %v", err)
		}
	}
	if err := validateDirectChangeReason(" \n "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason accepted: %v", err)
	}
}
