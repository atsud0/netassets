package textlength

import "testing"

// TestCountMeasuresCharactersNotBytes is the guard for the reported defect. The UI
// advertises limits in 字 (characters); len() counted UTF-8 bytes, so a Chinese
// reason was rejected at roughly a third of the advertised length.
func TestCountMeasuresCharactersNotBytes(t *testing.T) {
	cases := map[string]struct {
		value string
		want  int
	}{
		"纯中文":         {value: "填报说明", want: 4},
		"255 个中文字":    {value: repeat("中", 255), want: 255},
		"5000 个中文字":   {value: repeat("中", 5000), want: 5000},
		"英文":          {value: "description", want: 11},
		"中英数字混排":      {value: "2026年Q3填报abc123", want: 15},
		"emoji 属于一个字": {value: "✅❌", want: 2},
		"空串":          {value: "", want: 0},
	}
	for name, tc := range cases {
		if got := Count(tc.value); got != tc.want {
			t.Fatalf("%s: Count(%d runes) = %d, want %d", name, tc.want, got, tc.want)
		}
	}
}

// TestExceedsUsesTheAdvertisedCharacterLimit is the boundary test: exactly the
// advertised number of Chinese characters must be accepted, one more rejected.
// Under the old byte-based check 255 中文字 failed outright.
func TestExceedsUsesTheAdvertisedCharacterLimit(t *testing.T) {
	if Exceeds(repeat("中", 255), 255) {
		t.Fatal("255 个中文字被判定超出 255 字上限")
	}
	if !Exceeds(repeat("中", 256), 255) {
		t.Fatal("256 个中文字未被判定超出 255 字上限")
	}
	if Exceeds(repeat("中", 5000), 5000) {
		t.Fatal("5000 个中文字被判定超出 5000 字上限")
	}
	if !Exceeds(repeat("中", 5001), 5000) {
		t.Fatal("5001 个中文字未被判定超出 5000 字上限")
	}
	if !Within("正常说明", 5000) {
		t.Fatal("普通说明被判定超出上限")
	}
	if Shorter("short", 10) != true {
		t.Fatal("Shorter(5 chars, 10) = false, want true")
	}
	if Shorter(repeat("a", 10), 10) {
		t.Fatal("Shorter(10 chars, 10) = true, want false")
	}
}

func repeat(s string, n int) string {
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, []rune(s)...)
	}
	return string(out)
}
