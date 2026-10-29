package inventorytask

import (
	"errors"
	"netassets/internal/customfield"
)

// InputValidationError carries actionable, public messages for task, draft and
// progress input. Untyped ErrInvalid callers retain their existing fallbacks.
type InputValidationError struct {
	Message string
}

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }

func invalidInput(message string) error { return &InputValidationError{Message: message} }

// The caller deduplicates and trims selections before validation, preserving the
// existing limits on distinct non-empty values rather than raw array length.
func validateScopeSelections(organizationIDs, requiredFields []string, assetNetworkScope string) error {
	if !validAssetNetworkScope(assetNetworkScope) {
		return invalidInput("请选择有效的网络资产核查范围")
	}
	if len(organizationIDs) == 0 {
		return invalidInput("请选择至少一个填报组织")
	}
	if len(organizationIDs) > 1000 {
		return invalidInput("填报组织最多选择 1000 个")
	}
	if len(requiredFields) > 100 {
		return invalidInput("填报内容最多选择 100 项")
	}
	for _, field := range requiredFields {
		switch field {
		case "business_systems", "data_security", "network_assets", "owner_contacts":
		default:
			return invalidInput("请选择有效的填报内容")
		}
	}
	if systems, assets := selectedResourceTypes(requiredFields); !systems && !assets {
		return invalidInput("请至少选择业务系统、数据安全属性或网络资产中的一项填报内容")
	}
	return nil
}

func mapDraftCustomValueError(err error) error {
	var validation *customfield.ValueValidationError
	if errors.As(err, &validation) {
		return invalidInput(validation.Message)
	}
	return ErrInvalid
}
