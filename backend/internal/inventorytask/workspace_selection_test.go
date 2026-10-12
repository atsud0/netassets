package inventorytask

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizeWorkspaceSelectionItems(t *testing.T) {
	items, err := normalizeWorkspaceSelectionItems([]WorkspaceSelectionItem{{ID: " draft-a ", ExpectedVersion: 1}, {ID: "draft-b", ExpectedVersion: 2}, {ID: "draft-a", ExpectedVersion: 9}})
	if err != nil {
		t.Fatalf("normalizeWorkspaceSelectionItems returned error: %v", err)
	}
	if len(items) != 2 || items[0].ID != "draft-a" || items[1].ID != "draft-b" || items[0].ExpectedVersion != 1 {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestNormalizeWorkspaceSelectionItemsRejectsInvalidInput(t *testing.T) {
	for _, input := range [][]WorkspaceSelectionItem{nil, {{ID: "", ExpectedVersion: 1}}, {{ID: "draft-a", ExpectedVersion: 0}}} {
		if _, err := normalizeWorkspaceSelectionItems(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid for %#v, got %v", input, err)
		}
	}
	tooMany := make([]WorkspaceSelectionItem, MaxWorkspaceSelectionItems+1)
	for index := range tooMany {
		tooMany[index] = WorkspaceSelectionItem{ID: fmt.Sprintf("draft-%d", index), ExpectedVersion: 1}
	}
	if _, err := normalizeWorkspaceSelectionItems(tooMany); !errors.Is(err, ErrInvalid) {
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
		{ErrEditConflict, "draft_edit_conflict"},
		{ErrInvalid, "invalid"},
		{errors.New("database unavailable"), "internal_error"},
	} {
		code, message := workspaceSelectionError(test.err)
		if code != test.code || message == "" {
			t.Fatalf("workspaceSelectionError(%v) = %q, %q", test.err, code, message)
		}
	}
}
