package user

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"netassets/internal/textlength"
)

type InputValidationError struct{ Message string }

func (err *InputValidationError) Error() string { return err.Message }
func (err *InputValidationError) Unwrap() error { return ErrInvalid }
func invalidInput(message string) error         { return &InputValidationError{Message: message} }

func validIdentifier(value string) bool {
	var identifier pgtype.UUID
	return identifier.Scan(value) == nil && identifier.Valid
}

func normalizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	var identifier pgtype.UUID
	if identifier.Scan(value) != nil || !identifier.Valid {
		return value
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", identifier.Bytes[0:4], identifier.Bytes[4:6], identifier.Bytes[6:8], identifier.Bytes[8:10], identifier.Bytes[10:16])
}

func validateUserFields(name, email, mobile, office string) error {
	switch {
	case name == "":
		return invalidInput("姓名不能为空")
	case textlength.Count(name) > 128:
		return invalidInput("姓名最多 128 字")
	case textlength.Count(email) > 255:
		return invalidInput("邮箱最多 255 字")
	case textlength.Count(mobile) > 64:
		return invalidInput("手机号码最多 64 字")
	case textlength.Count(office) > 64:
		return invalidInput("办公电话最多 64 字")
	}
	return nil
}

func validateUserPassword(password, label string) error {
	if textlength.Count(password) < 10 {
		return invalidInput(fmt.Sprintf("%s至少 10 位", label))
	}
	if len(password) > 72 {
		return invalidInput(fmt.Sprintf("%s最多 72 字节", label))
	}
	return nil
}
