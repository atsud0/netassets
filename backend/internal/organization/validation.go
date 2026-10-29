package organization

import "netassets/internal/textlength"

// ValidationError carries only product-defined field messages and keeps the
// ErrInvalid contract for callers that do not need field-level detail.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }
func invalidField(message string) error  { return &ValidationError{Message: message} }

// Inputs are trimmed before validation, consistently for create and update.
func validateBasicInfo(name, shortName, manager, notes string) error {
	switch {
	case name == "" || textlength.Count(name) > 255:
		return invalidField("组织名称：请输入 1 至 255 字")
	case textlength.Count(shortName) > 128:
		return invalidField("组织简称：最多 128 字")
	case textlength.Count(manager) > 128:
		return invalidField("负责人：最多 128 字")
	case textlength.Count(notes) > 5000:
		return invalidField("备注：最多 5000 字")
	default:
		return nil
	}
}
