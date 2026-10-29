package securityworkorder

import (
	"time"

	"netassets/internal/textlength"
)

// InputValidationError contains a public field hint, never a database error.
// Keep ErrInvalid in its chain so existing import and HTTP contracts still apply.
type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }

func invalidField(message string) error { return &InputValidationError{Message: message} }

// Inputs are normalized by the service before validation. Validation ordering and
// limits match the former boolean checks; authorization must run before these.
func validateFindingInput(input FindingInput) error {
	switch {
	case input.Title == "":
		return invalidField("漏洞标题不能为空")
	case textlength.Count(input.Title) > 255:
		return invalidField("漏洞标题最多 255 字")
	case !validSeverity(input.Severity):
		return invalidField("请选择有效的严重等级")
	case !validFindingStatus(input.Status):
		return invalidField("请选择有效的安全发现状态")
	case !validUUID(input.AssigneeOrganizationID):
		return invalidField("请选择有效的责任组织")
	case input.AssetID == "" && input.BusinessSystemID == "":
		return invalidField("请至少关联一项网络资产或业务系统")
	case input.AssetID != "" && !validUUID(input.AssetID):
		return invalidField("关联网络资产标识不正确")
	case input.BusinessSystemID != "" && !validUUID(input.BusinessSystemID):
		return invalidField("关联业务系统标识不正确")
	case input.TestBatchID != "" && !validUUID(input.TestBatchID):
		return invalidField("关联测试批次标识不正确")
	case textlength.Count(input.VulnerabilityType) > 128:
		return invalidField("漏洞类型最多 128 字")
	case input.Description == "":
		return invalidField("漏洞描述不能为空")
	case textlength.Count(input.Description) > 20000:
		return invalidField("漏洞描述最多 20000 字")
	case textlength.Count(input.Evidence) > 30000:
		return invalidField("漏洞证据最多 30000 字")
	case textlength.Count(input.RemediationAdvice) > 20000:
		return invalidField("修复建议最多 20000 字")
	case input.Source != "manual" && input.Source != "import" && input.Source != "api" && input.Source != "plugin":
		return invalidField("请选择有效的安全发现来源")
	default:
		return nil
	}
}

func validateWorkOrderInput(input WorkOrderInput) error {
	switch {
	case input.Title == "":
		return invalidField("工单标题不能为空")
	case textlength.Count(input.Title) > 255:
		return invalidField("工单标题最多 255 字")
	case !validUUID(input.AssigneeOrganizationID):
		return invalidField("请选择有效的责任组织")
	case textlength.Count(input.Description) > 10000:
		return invalidField("工单说明最多 10000 字")
	case input.RemediationRequirement == "":
		return invalidField("整改要求不能为空")
	case textlength.Count(input.RemediationRequirement) > 20000:
		return invalidField("整改要求最多 20000 字")
	case input.DueAt.IsZero():
		return invalidField("请选择整改截止时间")
	// Preserve the existing one-minute clock-skew tolerance.
	case !input.DueAt.After(time.Now().Add(-time.Minute)):
		return invalidField("整改截止时间不能早于当前时间")
	case len(input.FindingIDs) == 0:
		return invalidField("请至少选择一条安全发现")
	case len(input.FindingIDs) > 100:
		return invalidField("单个工单最多关联 100 条安全发现")
	}
	for _, id := range input.FindingIDs {
		if !validUUID(id) {
			return invalidField("关联安全发现标识不正确")
		}
	}
	return nil
}

func validateRiskAcceptanceInput(input RiskAcceptanceInput) error {
	switch {
	case input.Reason == "":
		return invalidField("风险接受原因不能为空")
	case textlength.Count(input.Reason) > 5000:
		return invalidField("风险接受原因最多 5000 字")
	case input.AcceptedUntil.IsZero():
		return invalidField("请选择风险有效期")
	case !input.AcceptedUntil.After(time.Now()):
		return invalidField("风险有效期必须晚于当前时间")
	default:
		return nil
	}
}
