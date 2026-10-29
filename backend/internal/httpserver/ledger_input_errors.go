package httpserver

import (
	"errors"

	"netassets/internal/asset"
	"netassets/internal/businesssystem"
)

// Only explicitly public field errors may replace the endpoint's generic
// invalid-input guidance. Never return err.Error() for internal/wrapped errors.
func assetInputErrorMessage(err error, fallback string) string {
	var field *asset.InputValidationError
	if errors.As(err, &field) {
		return field.Message
	}
	return fallback
}

func businessSystemInputErrorMessage(err error, fallback string) string {
	var field *businesssystem.InputValidationError
	if errors.As(err, &field) {
		return field.Message
	}
	return fallback
}
