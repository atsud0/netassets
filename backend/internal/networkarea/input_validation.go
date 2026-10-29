package networkarea

import (
	"regexp"
	"strings"

	"netassets/internal/textlength"
)

type InputValidationError struct {
	Message string
}

func (err *InputValidationError) Error() string { return err.Message }
func (err *InputValidationError) Unwrap() error { return ErrInvalid }

var networkAreaUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func invalidField(message string) error {
	return &InputValidationError{Message: message}
}

func validateOrganizationID(id string, required bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		if required {
			return invalidField("请选择所属组织")
		}
		return nil
	}
	if !networkAreaUUID.MatchString(id) {
		return invalidField("所属组织标识不正确")
	}
	return nil
}

func validateFields(input Input) error {
	if input.Name == "" {
		return invalidField("请输入网络区域名称")
	}
	if textlength.Count(input.Name) > 128 {
		return invalidField("网络区域名称最多 128 字")
	}
	if textlength.Count(input.Code) > 64 {
		return invalidField("标识 / 地址范围最多 64 字")
	}
	if textlength.Count(input.Notes) > 5000 {
		return invalidField("说明最多 5000 字")
	}
	if input.SortOrder < 0 || input.SortOrder > 100000 {
		return invalidField("排序须为 0 至 100000 的整数")
	}
	if input.Status != "enabled" && input.Status != "disabled" {
		return invalidField("请选择有效的状态")
	}
	return nil
}
