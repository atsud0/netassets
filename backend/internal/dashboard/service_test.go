package dashboard

import "testing"

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
