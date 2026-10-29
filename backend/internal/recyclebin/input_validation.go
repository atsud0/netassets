package recyclebin

type InputValidationError struct{ Message string }

func (err *InputValidationError) Error() string { return err.Message }
func (err *InputValidationError) Unwrap() error { return ErrInvalid }

func invalidField(message string) error { return &InputValidationError{Message: message} }
