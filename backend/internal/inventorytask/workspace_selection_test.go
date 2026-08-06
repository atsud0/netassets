package inventorytask

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizeWorkspaceSelectionIDs(t *testing.T) {
	ids, err := normalizeWorkspaceSelectionIDs([]string{" draft-a ", "draft-b", "draft-a"})
	if err != nil {
		t.Fatalf("normalizeWorkspaceSelectionIDs returned error: %v", err)
	}
	if len(ids) != 2 || ids[0] != "draft-a" || ids[1] != "draft-b" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
}

func TestNormalizeWorkspaceSelectionIDsRejectsInvalidInput(t *testing.T) {
	if _, err := normalizeWorkspaceSelectionIDs(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty input, got %v", err)
	}
	if _, err := normalizeWorkspaceSelectionIDs([]string{""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for blank id, got %v", err)
	}
	tooMany := make([]string, MaxWorkspaceSelectionItems+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("draft-%d", index)
	}
	if _, err := normalizeWorkspaceSelectionIDs(tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for oversized input, got %v", err)
	}
}

func TestWorkspaceSelectionError(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{ErrForbidden, "forbidden"},
		{ErrNotFound, "not_found"},
		{ErrState, "invalid_state"},
		{ErrInvalid, "invalid"},
		{errors.New("database unavailable"), "internal_error"},
	} {
		code, message := workspaceSelectionError(test.err)
		if code != test.code || message == "" {
			t.Fatalf("workspaceSelectionError(%v) = %q, %q", test.err, code, message)
		}
	}
}
