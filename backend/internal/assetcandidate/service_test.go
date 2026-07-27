package assetcandidate

import "testing"

func TestValidStatus(t *testing.T) {
	for _, status := range []string{"pending", "accepted", "ignored", "duplicate", "invalid"} {
		if !validStatus(status) {
			t.Fatalf("status %q should be valid", status)
		}
	}
	if validStatus("reviewing") {
		t.Fatal("unexpected valid status")
	}
}
