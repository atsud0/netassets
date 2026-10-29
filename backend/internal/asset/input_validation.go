package asset

import "netassets/internal/textlength"

// InputValidationError identifies a public input field without exposing values
// or database details, while retaining the existing ErrInvalid category.
type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }
func invalidInput(message string) error       { return &InputValidationError{Message: message} }

func validateCreateFields(input CreateInput) error {
	switch {
	case input.OrganizationID == "":
		return invalidInput("请选择所属组织")
	case !assetUUIDPattern.MatchString(input.OrganizationID):
		return invalidInput("所属组织标识：请选择有效的组织")
	case input.BusinessSystemID != "" && !assetUUIDPattern.MatchString(input.BusinessSystemID):
		return invalidInput("业务系统标识：请选择有效的业务系统或工作版本")
	case input.NetworkAreaID != "" && !assetUUIDPattern.MatchString(input.NetworkAreaID):
		return invalidInput("网络区域标识：请选择有效的网络区域")
	case input.OwnerUserID != "" && !assetUUIDPattern.MatchString(input.OwnerUserID):
		return invalidInput("责任人账号标识：请选择有效的账号")
	case !oneOf(input.Type, "domain", "ip", "url", "service", "other"):
		return invalidInput("请选择有效的资产类型")
	case input.Value == "":
		return invalidInput("资产值不能为空")
	case !oneOf(input.Environment, "production", "staging", "testing", "development", "office", "other"):
		return invalidInput("请选择有效的部署环境")
	case !oneOf(input.NetworkScope, "public", "private", "unknown"):
		return invalidInput("请选择有效的网络范围")
	case !oneOf(input.Status, "active", "pending", "disabled", "archived"):
		return invalidInput("请选择有效的资产状态")
	case !oneOf(input.Importance, "normal", "important", "core"):
		return invalidInput("请选择有效的资产重要程度")
	case textlength.Exceeds(input.Name, 255):
		return invalidInput("资产名称最多 255 字")
	case textlength.Exceeds(input.OwnerName, 128):
		return invalidInput("负责人最多 128 字")
	}
	return nil
}
