package securitytestrequest

import (
	"strings"

	"netassets/internal/textlength"
)

type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }
func (e *InputValidationError) Unwrap() error { return ErrInvalid }
func invalidInput(message string) error       { return &InputValidationError{Message: message} }

func validateInput(input Input) error {
	input.BusinessSystemID = strings.TrimSpace(input.BusinessSystemID)
	input.BusinessSystemDraftID = strings.TrimSpace(input.BusinessSystemDraftID)
	if (input.BusinessSystemID == "") == (input.BusinessSystemDraftID == "") {
		return invalidInput("请选择一个上线业务系统，正式版本与工作版本不能同时选择")
	}
	if input.RequestName == "" || textlength.Count(input.RequestName) > 255 {
		return invalidInput("申请名称不能为空且不能超过 255 个字符")
	}
	if input.Background == "" || textlength.Count(input.Background) > 10000 {
		return invalidInput("申请背景不能为空且不能超过 10000 个字符")
	}
	if !validTestType(input.TestType) {
		return invalidInput("请选择有效的测试类型")
	}
	if input.ExpectedLaunchAt.IsZero() {
		return invalidInput("请选择期望上线时间")
	}
	if !validUUID(input.ContactUserID) {
		return invalidInput("请选择有效的联系人账号")
	}
	if input.BusinessSystemID != "" && !validUUID(input.BusinessSystemID) {
		return invalidInput("上线业务系统标识格式不正确")
	}
	if input.BusinessSystemDraftID != "" && !validUUID(input.BusinessSystemDraftID) {
		return invalidInput("上线业务系统工作版本标识格式不正确")
	}
	if input.TestWindowStartAt != nil && input.TestWindowEndAt != nil && !input.TestWindowEndAt.After(*input.TestWindowStartAt) {
		return invalidInput("测试结束时间必须晚于开始时间")
	}
	if total := len(input.WorkingAssetIDs) + len(input.PublishedAssetIDs); total < 1 || total > maxScopeAssets {
		return invalidInput("申请范围需包含 1 至 200 项资产")
	}
	for _, resourceIDs := range [][]string{input.WorkingAssetIDs, input.PublishedAssetIDs} {
		for _, resourceID := range resourceIDs {
			if !validUUID(resourceID) {
				return invalidInput("申请范围内的资产标识格式不正确")
			}
		}
	}
	return nil
}
