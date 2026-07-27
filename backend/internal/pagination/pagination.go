// Package pagination contains the common, bounded page contract used by list APIs.
package pagination

import "errors"

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

var ErrInvalid = errors.New("invalid pagination")

// Query describes a one-based page request. All public list endpoints use this
// shape and never treat a missing page_size as an unbounded request.
type Query struct {
	Page     int
	PageSize int
}

func Normalize(page, pageSize int) (Query, error) {
	if page == 0 {
		page = DefaultPage
	}
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > MaxPageSize {
		return Query{}, ErrInvalid
	}
	return Query{Page: page, PageSize: pageSize}, nil
}

func (q Query) Offset() int { return (q.Page - 1) * q.PageSize }

// Result is the standard response payload for all collection endpoints.
type Result[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func NewResult[T any](items []T, total int, query Query) Result[T] {
	if items == nil {
		items = []T{}
	}
	return Result[T]{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}
}

// NeedsTotalFallback reports whether a window-count query returned no row from
// which to read its total. This only occurs on a page beyond the last page;
// callers should then run their matching COUNT query so total remains real.
func NeedsTotalFallback(itemCount int, query Query) bool {
	return itemCount == 0 && query.Page > 1
}
