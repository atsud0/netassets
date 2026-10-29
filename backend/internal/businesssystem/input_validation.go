package businesssystem

import "netassets/internal/textlength"

// InputValidationError is safe to publish; arbitrary internal errors are not.
type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }
func invalidInput(message string) error       { return &InputValidationError{Message: message} }

// ValidateOptionalTextFields enforces formal-ledger storage limits. Callers
// normalize new input first; publication also checks persisted legacy drafts,
// whose deployment column historically allowed 64 instead of 32 characters.
func ValidateOptionalTextFields(input CreateInput) error {
	switch {
	case textlength.Count(input.ShortName) > 128:
		return invalidInput("系统简称最多 128 字")
	case textlength.Count(input.OwnerName) > 128:
		return invalidInput("负责人最多 128 字")
	case textlength.Count(input.DeploymentType) > 32:
		return invalidInput("部署方式最多 32 字")
	}
	return nil
}

func validateCreateFields(input CreateInput) error {
	if err := ValidateOptionalTextFields(input); err != nil {
		return err
	}
	switch {
	case !codePattern.MatchString(input.Code):
		return invalidInput("系统编码须为 2–64 位字母、数字、下划线或横线，且以字母或数字开头")
	case input.Name == "":
		return invalidInput("系统名称不能为空")
	case textlength.Count(input.Name) > 255:
		return invalidInput("系统名称最多 255 字")
	case input.OrganizationID == "":
		return invalidInput("请选择所属组织")
	case !businessSystemUUIDPattern.MatchString(input.OrganizationID):
		return invalidInput("所属组织标识：请选择有效的组织")
	case !validOwnerContact(input.OwnerContact):
		return ErrInvalidOwnerContact
	case !oneOf(input.Status, "planning", "running", "suspended", "offline", "archived"):
		return invalidInput("请选择有效的系统状态")
	case !oneOf(input.Level, "normal", "important", "core"):
		return invalidInput("请选择有效的系统等级")
	case !oneOf(input.InternetExposure, "yes", "no", "partial", "unknown"):
		return invalidInput("请选择有效的互联网暴露情况")
	case !oneOf(input.ContainsPersonalInformation, "yes", "no", "unknown"):
		return invalidInput("请选择有效的是否包含个人信息选项")
	case !oneOf(input.ContainsSensitivePersonalInformation, "yes", "no", "unknown"):
		return invalidInput("请选择有效的是否包含敏感个人信息选项")
	case input.ContainsPersonalInformation == "no" && input.ContainsSensitivePersonalInformation == "yes":
		return invalidInput("包含敏感个人信息时，是否包含个人信息不能选择否")
	case input.PersonalInformationRecordCount != nil && *input.PersonalInformationRecordCount < 0:
		return invalidInput("个人信息记录数量不能小于 0")
	case !oneOf(input.DataVolumeOver100K, "yes", "no", "unknown"):
		return invalidInput("请选择有效的数据量是否超过 10 万条选项")
	case !oneOf(input.ContainsImportantData, "yes", "no", "unknown", "pending"):
		return invalidInput("请选择有效的是否包含重要数据选项")
	case !oneOf(input.ContainsCoreData, "yes", "no", "unknown", "pending"):
		return invalidInput("请选择有效的是否包含核心数据选项")
	}
	return nil
}
