package httpserver

import (
	"errors"

	"netassets/internal/auth"
)

func authFieldMessage(err error, fallback string) string {
	var field *auth.FieldError
	if errors.As(err, &field) {
		return field.Message
	}
	return fallback
}
