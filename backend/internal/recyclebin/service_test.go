package recyclebin

import "testing"

func TestValidUUID(t *testing.T) {
	for _, value := range []string{
		"b1dcab42-a13a-43a9-991e-3d0d75ad3a0e",
		"B1DCAB42-A13A-43A9-991E-3D0D75AD3A0E",
	} {
		if !validUUID(value) {
			t.Fatalf("validUUID(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "123", "b1dcab42-a13a-43a9-991e-3d0d75ad3a0", "not-a-uuid"} {
		if validUUID(value) {
			t.Fatalf("validUUID(%q) = true, want false", value)
		}
	}
}
