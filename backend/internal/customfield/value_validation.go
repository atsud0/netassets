package customfield

import "fmt"

// ValueValidationError refers only to a published field title, never its value.
type ValueValidationError struct{ Message string }

func (e *ValueValidationError) Error() string { return e.Message }
func (e *ValueValidationError) Unwrap() error { return ErrInvalid }
func invalidValue(message string) error       { return &ValueValidationError{Message: message} }
func fieldValueError(definition Definition, suffix string) error {
	name := definition.FieldName
	if name == "" {
		name = definition.FieldKey
	}
	return invalidValue(fmt.Sprintf("自定义字段“%s”%s", name, suffix))
}
