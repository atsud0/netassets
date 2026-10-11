package dashboard

import (
	"strings"
	"testing"
)

func TestPercentage(t *testing.T) {
	tests := []struct {
		name        string
		numerator   int64
		denominator int64
		want        int64
	}{
		{name: "whole", numerator: 8, denominator: 8, want: 100},
		{name: "truncates fractional result", numerator: 2, denominator: 3, want: 66},
		{name: "zero denominator", numerator: 1, denominator: 0, want: 0},
		{name: "negative denominator", numerator: 1, denominator: -1, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := percentage(tt.numerator, tt.denominator); got != tt.want {
				t.Fatalf("percentage(%d, %d) = %d, want %d", tt.numerator, tt.denominator, got, tt.want)
			}
		})
	}
}

func TestPendingAssetFinding(t *testing.T) {
	got := pendingAssetFinding("f")
	for _, fragment := range []string{"f.asset_id IS NULL", "f.pending_asset_draft_id IS NOT NULL"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("pendingAssetFinding(%q) = %q, want it to contain %q", "f", got, fragment)
		}
	}
	if strings.Contains(got, "pending_asset_draft_id IS NULL") {
		t.Fatalf("pendingAssetFinding(%q) = %q, must only count findings that have a working version", "f", got)
	}
}

// A finding on a retired asset is not a pending asset and not an unlinked one. The
// asset was classified and then deleted on purpose; re-registering it would be
// wrong advice, so the predicate must require the soft-delete marker rather than
// a missing asset_id.
func TestRetiredAssetFinding(t *testing.T) {
	got := retiredAssetFinding("f", "retired_asset")
	for _, want := range []string{"f.asset_id IS NOT NULL", "retired_asset.deleted_at IS NOT NULL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("retiredAssetFinding() = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "pending_asset_draft_id") {
		t.Fatalf("retiredAssetFinding() should not depend on the working version column: %q", got)
	}
}
