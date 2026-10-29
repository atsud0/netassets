package httpserver

import (
	"errors"

	"netassets/internal/networkarea"
)

func networkAreaInputMessage(err error, fallback string) string {
	var fieldError *networkarea.InputValidationError
	if errors.As(err, &fieldError) {
		return fieldError.Message
	}
	return fallback
}
