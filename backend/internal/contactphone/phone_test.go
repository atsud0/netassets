package contactphone

import "testing"

// TestIsValidRejectsUnusableContactNumbers is the guard for the reported defect:
// the owner-contact field accepted any text, so "abc" or a stray five digit
// fragment entered the ledger and could never reach the system owner.
func TestIsValidRejectsUnusableContactNumbers(t *testing.T) {
	for _, value := range []string{"abc", "张三", "1380000000", "12345678901", "138000000001234", "转接行政", "138-0000-0000a", "+86 138 0000 0000 ext"} {
		if IsValid(value) {
			t.Fatalf("IsValid(%q) = true, want false", value)
		}
	}
}

func TestIsValidAcceptsRealContactNumbers(t *testing.T) {
	for _, value := range []string{"13800000000", "138-0000-0000", "138 0000 0000", "(021)88886666", "021-88886666", "0755-88886666-8001", "400-820-8820", "8008208820", "+8613800000000", "6688", ""} {
		if !IsValid(value) {
			t.Fatalf("IsValid(%q) = false, want true", value)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		" 138-0000-0000 ": "13800000000",
		"(021) 8888 6666": "02188886666",
		"021 8888 6666":   "02188886666",
		"":                "",
		"  ":              "",
		"转接行政":            "转接行政",
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMaxLengthIsEnforced(t *testing.T) {
	long := ""
	for i := 0; i <= MaxLength; i++ {
		long += "1"
	}
	if len(long) > MaxLength && IsValid(long) {
		t.Fatalf("a %d character value must not be considered a usable contact number", len(long))
	}
}
