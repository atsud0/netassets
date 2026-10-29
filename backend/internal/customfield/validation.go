package customfield

import "netassets/internal/textlength"

// DefinitionValidationError preserves the existing ErrInvalid contract while
// letting the API identify the offending configuration field. Authorization is
// checked before validation by both write methods.
type DefinitionValidationError struct{ Message string }

func (e *DefinitionValidationError) Error() string { return e.Message }
func (e *DefinitionValidationError) Unwrap() error { return ErrInvalid }
func invalidDefinition(message string) error       { return &DefinitionValidationError{Message: message} }

func validateDefinition(in DefinitionInput) error {
	switch {
	case in.ResourceType != "asset" && in.ResourceType != "business_system":
		return invalidDefinition("请选择有效的对象类型")
	case !keyPattern.MatchString(in.FieldKey):
		return invalidDefinition("字段编码须为 2–64 位小写字母、数字或下划线，且以字母开头")
	case in.FieldName == "":
		return invalidDefinition("字段名称不能为空")
	case textlength.Count(in.FieldName) > 128:
		return invalidDefinition("字段名称最多 128 字")
	case in.SortOrder < 0 || in.SortOrder > 100000:
		return invalidDefinition("排序须为 0 至 100000 的整数")
	}
	switch in.FieldType {
	case "text", "textarea", "number", "date", "boolean":
		if len(in.Options) != 0 {
			return invalidDefinition("当前字段类型不支持可选项")
		}
	case "select", "multiselect":
		if len(in.Options) < 1 || len(in.Options) > 100 {
			return invalidDefinition("可选项须为 1 至 100 项")
		}
	default:
		return invalidDefinition("请选择有效的字段类型")
	}
	return nil
}

func validateFieldValue(definition Definition, value any) error {
	if validFieldValue(definition, value) {
		return nil
	}
	prefix := "自定义字段“" + definition.FieldName + "”："
	if isBlank(value) && definition.IsRequired {
		return invalidDefinition(prefix + "不能为空")
	}
	switch definition.FieldType {
	case "text", "textarea", "date":
		return invalidDefinition(prefix + "须为文本")
	case "number":
		return invalidDefinition(prefix + "须为数字")
	case "boolean":
		return invalidDefinition(prefix + "须为布尔值")
	case "select":
		return invalidDefinition(prefix + "请选择配置中的有效选项")
	case "multiselect":
		return invalidDefinition(prefix + "须为有效选项组成的数组")
	default:
		return invalidDefinition(prefix + "字段类型不受支持")
	}
}
