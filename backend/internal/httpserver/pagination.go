package httpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"netassets/internal/pagination"
)

// parsePageQuery keeps every collection endpoint on the same bounded contract.
// Callers should return a 400 with their domain-specific error message on error.
func parsePageQuery(c *gin.Context) (pagination.Query, error) {
	page, err := optionalPositiveInt(c.Query("page"))
	if err != nil {
		return pagination.Query{}, &pageQueryValidationError{message: "页码（page）：须为大于 0 的整数，且不能超过系统支持的范围"}
	}
	pageSize, err := optionalPositiveInt(c.Query("page_size"))
	if err != nil || pageSize > pagination.MaxPageSize {
		return pagination.Query{}, &pageQueryValidationError{message: fmt.Sprintf("每页条数（page_size）：须为 1 至 %d 的整数", pagination.MaxPageSize)}
	}
	query, err := pagination.Normalize(page, pageSize)
	if err != nil {
		return pagination.Query{}, &pageQueryValidationError{message: "页码（page）：超出系统支持的范围，请减小页码"}
	}
	return query, nil
}

type pageQueryValidationError struct{ message string }

func (err *pageQueryValidationError) Error() string { return err.message }
func (err *pageQueryValidationError) Unwrap() error { return pagination.ErrInvalid }

func pageQueryErrorMessage(err error) string {
	var validation *pageQueryValidationError
	if errors.As(err, &validation) {
		return validation.message
	}
	return "分页参数不正确"
}

func optionalPositiveInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, pagination.ErrInvalid
	}
	return parsed, nil
}

// parseOptionalBoolQuery accepts an omitted flag as false and rejects ambiguous values.
func parseOptionalBoolQuery(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false":
		return false, true
	case "1", "true":
		return true, true
	default:
		return false, false
	}
}
