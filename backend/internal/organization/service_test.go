package organization

import "testing"

func TestBuildTree(t *testing.T) {
	rootID := "root"
	childID := "child"
	items := []Organization{
		{ID: "grandchild", ParentID: &childID, Code: "003"},
		{ID: rootID, Code: "001", Type: "group"},
		{ID: childID, ParentID: &rootID, Code: "002"},
	}
	tree := BuildTree(items)
	if len(tree) != 1 || tree[0].ID != rootID {
		t.Fatalf("BuildTree roots = %+v, want root", tree)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].ID != childID {
		t.Fatalf("BuildTree children = %+v, want child", tree[0].Children)
	}
	if len(tree[0].Children[0].Children) != 1 || tree[0].Children[0].Children[0].ID != "grandchild" {
		t.Fatalf("BuildTree grandchildren = %+v, want grandchild", tree[0].Children[0].Children)
	}
}

func TestFinalizeImpact(t *testing.T) {
	impact := Impact{OrganizationType: "subsidiary", Status: "enabled", UserCount: 2, AssetCount: 3}
	finalizeImpact(&impact)
	if !impact.CanDisable || !impact.RequiresResourceConfirmation {
		t.Fatalf("resource-only impact = %+v, want disable with confirmation", impact)
	}
	if impact.CanDelete {
		t.Fatalf("enabled non-empty organization can delete: %+v", impact)
	}

	blocked := Impact{OrganizationType: "subsidiary", Status: "enabled", EnabledChildCount: 1, OpenWorkOrderCount: 1}
	finalizeImpact(&blocked)
	if blocked.CanDisable || len(blocked.DisableBlockers) != 2 {
		t.Fatalf("blocked impact = %+v, want two disable blockers", blocked)
	}

	empty := Impact{OrganizationType: "department", Status: "disabled"}
	finalizeImpact(&empty)
	if !empty.CanDelete {
		t.Fatalf("empty disabled organization = %+v, want deletable", empty)
	}
}

func TestValidNonGroupType(t *testing.T) {
	for _, value := range []string{"subsidiary", "department", "other"} {
		if !validNonGroupType(value) {
			t.Fatalf("validNonGroupType(%q) = false", value)
		}
	}
	for _, value := range []string{"", "group", "company"} {
		if validNonGroupType(value) {
			t.Fatalf("validNonGroupType(%q) = true", value)
		}
	}
}
