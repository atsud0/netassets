package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeAPITokenInput(t *testing.T) {
	input, err := NormalizeAPITokenInput(CreateAPITokenInput{Name: " 资产同步脚本 ", ExpiresInDays: 90})
	if err != nil {
		t.Fatalf("NormalizeAPITokenInput() error = %v", err)
	}
	if input.Name != "资产同步脚本" || input.ExpiresInDays != 90 {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestNormalizeAPITokenInputRejectsInvalidValues(t *testing.T) {
	cases := []CreateAPITokenInput{
		{Name: "", ExpiresInDays: 90},
		{Name: strings.Repeat("字", 101), ExpiresInDays: 90},
		{Name: "脚本", ExpiresInDays: 0},
		{Name: "脚本", ExpiresInDays: 366},
	}
	for index, input := range cases {
		if _, err := NormalizeAPITokenInput(input); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d error = %v, want ErrInvalid", index, err)
		}
	}
}

func TestAPITokenUUIDPattern(t *testing.T) {
	if !apiTokenUUIDPattern.MatchString("2cf486e9-71c8-4a48-82e4-17e3ab76b53d") {
		t.Fatal("valid UUID rejected")
	}
	for _, value := range []string{"", "not-a-uuid", "2cf486e9-71c8-0a48-82e4-17e3ab76b53d"} {
		if apiTokenUUIDPattern.MatchString(value) {
			t.Errorf("invalid UUID %q accepted", value)
		}
	}
}
