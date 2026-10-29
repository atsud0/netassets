package httpserver

import (
	"errors"
	"fmt"
	"testing"

	"netassets/internal/asset"
	"netassets/internal/businesssystem"
)

func TestLedgerInputErrorMessagesExposeOnlyPublicFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message func(error, string) string
		invalid error
		public  error
	}{
		{"asset", assetInputErrorMessage, asset.ErrInvalid, &asset.InputValidationError{Message: "请选择有效的部署环境"}},
		{"business system", businessSystemInputErrorMessage, businesssystem.ErrInvalid, &businesssystem.InputValidationError{Message: "系统名称不能为空"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, err := range []error{tc.public, fmt.Errorf("prepare input: %w", tc.public)} {
				if got := tc.message(err, "fallback"); got != tc.public.Error() {
					t.Fatalf("public message lost: %q", got)
				}
			}
			for _, err := range []error{nil, tc.invalid, errors.New("internal database details"), errors.Join(tc.invalid, errors.New("internal database details"))} {
				if got := tc.message(err, "fallback"); got != "fallback" {
					t.Fatalf("unsafe message: %q", got)
				}
			}
		})
	}
}
