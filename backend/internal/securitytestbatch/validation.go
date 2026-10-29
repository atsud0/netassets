package securitytestbatch

import (
	"fmt"
	"strings"

	"netassets/internal/textlength"
)

// ValidationError preserves ErrInvalid while exposing only product-defined field
// messages. Callers must not surface database/internal error strings to clients.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func validateTransitionComment(action, comment string) error {
	label := "操作说明"
	if action == "cancel" {
		label = "取消原因"
		// Preserve cancellation's existing trim-before-count behavior.
		comment = strings.TrimSpace(comment)
		if comment == "" {
			return &ValidationError{Message: "取消原因不能为空"}
		}
	}
	if textlength.Count(comment) > 2000 {
		return &ValidationError{Message: label + "最多 2000 字"}
	}
	return nil
}

// validateInput runs after normalization and before any transaction or write.
// Keep existing character limits, scope deduplication and historical-date support.
func validateInput(input Input) error {
	for _, field := range []struct {
		label, value string
		limit        int
		required     bool
	}{
		{"批次名称", input.Name, 255, true},
		{"测试单位", input.Provider, 255, true},
		{"授权说明", input.AuthorizationNote, 20000, true},
		{"测试约束", input.Notes, 20000, false},
	} {
		if field.required && field.value == "" {
			return &ValidationError{Message: field.label + "不能为空"}
		}
		if textlength.Count(field.value) > field.limit {
			return &ValidationError{Message: fmt.Sprintf("%s最多 %d 字", field.label, field.limit)}
		}
	}
	if !validTestType(input.TestType) {
		return &ValidationError{Message: "请选择有效的测试类型"}
	}
	if input.StartAt.IsZero() {
		return &ValidationError{Message: "请选择计划开始时间"}
	}
	if input.EndAt.IsZero() {
		return &ValidationError{Message: "请选择计划结束时间"}
	}
	if !input.EndAt.After(input.StartAt) {
		return &ValidationError{Message: "计划结束时间必须晚于计划开始时间"}
	}
	if len(input.AssetIDs) == 0 {
		return &ValidationError{Message: "请至少选择 1 项公网资产"}
	}
	if len(input.AssetIDs) > maxScopeAssets {
		return &ValidationError{Message: fmt.Sprintf("公网资产范围最多 %d 项", maxScopeAssets)}
	}
	for _, id := range input.AssetIDs {
		if !validUUID(id) {
			return &ValidationError{Message: "公网资产范围包含无效的资产标识"}
		}
	}
	return nil
}
