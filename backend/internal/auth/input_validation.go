package auth

import (
	"strings"

	"netassets/internal/textlength"
)

type FieldError struct {
	Message string
}

func (err *FieldError) Error() string { return err.Message }
func (err *FieldError) Unwrap() error { return ErrInvalid }

func normalizeProfileInput(input ProfileInput) (ProfileInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.MobilePhone = strings.TrimSpace(input.MobilePhone)
	input.OfficePhone = strings.TrimSpace(input.OfficePhone)
	if input.DisplayName == "" {
		return ProfileInput{}, &FieldError{Message: "显示名称不能为空"}
	}
	for _, field := range []struct {
		value   string
		limit   int
		message string
	}{
		{input.DisplayName, 128, "显示名称最多 128 字"},
		{input.Email, 255, "邮箱最多 255 字"},
		{input.MobilePhone, 64, "手机号码最多 64 字"},
		{input.OfficePhone, 64, "办公电话最多 64 字"},
	} {
		if textlength.Count(field.value) > field.limit {
			return ProfileInput{}, &FieldError{Message: field.message}
		}
	}
	return input, nil
}

func validatePasswordChange(input ChangePasswordInput) error {
	if input.CurrentPassword == "" {
		return &FieldError{Message: "当前密码不能为空"}
	}
	if textlength.Count(input.NewPassword) < 10 {
		return &FieldError{Message: "新密码至少需要 10 位"}
	}
	if len(input.NewPassword) > 72 {
		return &FieldError{Message: "新密码最多 72 字节"}
	}
	return nil
}
