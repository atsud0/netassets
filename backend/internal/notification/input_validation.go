package notification

import "strings"

type ValidationError struct{ Message string }

func (err *ValidationError) Error() string { return err.Message }

func (err *ValidationError) Unwrap() error { return ErrInvalid }

func validateReadKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return &ValidationError{Message: "消息标识不能为空"}
	}
	if len(strings.TrimSpace(key)) > 255 {
		return &ValidationError{Message: "消息标识最多 255 字节"}
	}
	return nil
}
