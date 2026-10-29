package remediation

import (
	"strconv"
	"strings"
	"testing"
)

func TestTextOKCountsCharactersAtAdvertisedLimits(t *testing.T) {
	for _, limit := range []int{128, 255, 2000, 10000, 20000} {
		for name, letter := range map[string]string{"Chinese": "因", "ASCII": "R", "astral": "🔒"} {
			t.Run(name+"/"+strconv.Itoa(limit), func(t *testing.T) {
				if !textOK(strings.Repeat(letter, limit), limit, true) {
					t.Errorf("%s: exactly %d characters rejected", name, limit)
				}
				if textOK(strings.Repeat(letter, limit+1), limit, true) {
					t.Errorf("%s: %d characters accepted at limit %d", name, limit+1, limit)
				}
			})
		}
	}
	for _, value := range []string{"", " \t\n", "\u3000"} {
		if textOK(value, 255, true) {
			t.Errorf("required whitespace %q accepted", value)
		}
		if !textOK(value, 255, false) {
			t.Errorf("optional whitespace %q rejected", value)
		}
	}
}
