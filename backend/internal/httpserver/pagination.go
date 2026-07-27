package httpserver

import (
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
		return pagination.Query{}, err
	}
	pageSize, err := optionalPositiveInt(c.Query("page_size"))
	if err != nil {
		return pagination.Query{}, err
	}
	return pagination.Normalize(page, pageSize)
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
