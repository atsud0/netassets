package httpserver

import (
	"reflect"
	"testing"
)

func TestSplitSelectedAssetIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace", raw: "   ", want: nil},
		{name: "values", raw: " id-a, id-b ", want: []string{"id-a", "id-b"}},
		{name: "empty member remains invalid", raw: "id-a,,id-b", want: []string{"id-a", "", "id-b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := splitSelectedAssetIDs(test.raw); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("splitSelectedAssetIDs(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}
