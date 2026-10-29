package assetcandidate

import "regexp"

var candidateUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// InputValidationError carries only deliberate public field guidance. It keeps
// the existing invalid-input category without exposing SQL or submitted values.
type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }
func invalidInput(message string) error       { return &InputValidationError{Message: message} }
