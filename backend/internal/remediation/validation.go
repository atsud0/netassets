package remediation

// ValidationError contains only product-defined field messages, never database errors.
// Unwrap preserves the existing ErrInvalid contract for non-HTTP callers.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }
func invalidField(message string) error  { return &ValidationError{Message: message} }
