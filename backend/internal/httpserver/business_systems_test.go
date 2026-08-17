package httpserver

import (
	"reflect"
	"testing"
)

func TestSplitSelectedBusinessSystemIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace", raw: "   ", want: nil},
		{name: "single", raw: "system-a", want: []string{"system-a"}},
		{name: "multiple and trimmed", raw: " system-a,system-b , system-c ", want: []string{"system-a", "system-b", "system-c"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := splitSelectedBusinessSystemIDs(test.raw); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("splitSelectedBusinessSystemIDs(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}
