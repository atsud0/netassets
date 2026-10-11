package inventorydraft

import "testing"

// The predicates are embedded into SQL, so a typo or a reordered set silently
// changes list visibility or breaks ON CONFLICT partial index inference.
func TestStatusPredicates(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"active", ActiveSQL, "('editing','submitted','returned')"},
		{"editable", EditableSQL, "('editing','returned')"},
		{"reviewable", ReviewableSQL, "('submitted','published')"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s predicate = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

func TestStateSets(t *testing.T) {
	active := ActiveStatuses()
	editable := EditableStatuses()
	if len(active) != 3 {
		t.Fatalf("active statuses = %v, want 3 entries", active)
	}
	for _, status := range editable {
		if status == Submitted {
			t.Error("submitted drafts must not be editable while under review")
		}
	}
	for _, status := range active {
		if status == Published || status == Discarded {
			t.Errorf("%q is terminal and must not be treated as an active draft", status)
		}
	}
}
