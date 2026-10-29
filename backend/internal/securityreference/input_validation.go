package securityreference

import "fmt"

type InputValidationError struct{ Message string }

func (err *InputValidationError) Error() string { return err.Message }
func (err *InputValidationError) Unwrap() error { return ErrInvalid }

func invalidInput(message string) error {
	return &InputValidationError{Message: message}
}

func validateAssetSelection(input AssetSelectionInput) error {
	label := ""
	switch input.Dimension {
	case "organization":
		label = "组织"
	case "business_system":
		label = "业务系统"
	case "network_area":
		label = "网络区域"
	default:
		return invalidInput("请选择有效的资产圈选维度：组织、业务系统或网络区域")
	}
	if len(input.IDs) == 0 {
		return invalidInput("请至少选择一个" + label)
	}
	if len(input.IDs) > maxSelectionIDs {
		return invalidInput(fmt.Sprintf("单次最多选择 %d 个%s", maxSelectionIDs, label))
	}
	for index, identifier := range input.IDs {
		if !uuidPattern.MatchString(identifier) {
			return invalidInput(fmt.Sprintf("第 %d 个%s标识不正确", index+1, label))
		}
	}
	return nil
}
