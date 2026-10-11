// Package findingstatus is the single source of truth for security finding
// status semantics. Dashboard breakdowns, notification labels, work order
// validation and remediation completion rules all read from here so that the
// same value cannot mean "closed" in one place and "open" in another.
package findingstatus

import "strings"

// Status values stored in security_findings.status.
const (
	Pending       = "pending"
	InProgress    = "in_progress"
	Confirmed     = "confirmed"
	Resolved      = "resolved"
	Fixed         = "fixed"
	FalsePositive = "false_positive"
	Ignored       = "ignored"
	Duplicate     = "duplicate"
	RiskAccepted  = "risk_accepted"
)

// Definition carries the persisted value together with its display label and
// the order used by status breakdowns.
type Definition struct {
	Value     string
	Label     string
	SortOrder int
}

// Definitions lists every status in presentation order.
func Definitions() []Definition {
	return []Definition{
		{Pending, "待处理", 1},
		{InProgress, "处理中", 2},
		{Confirmed, "已确认", 3},
		{Resolved, "已处理", 4},
		{Fixed, "已修复", 5},
		{FalsePositive, "误报", 6},
		{Ignored, "忽略", 7},
		{Duplicate, "重复", 8},
		{RiskAccepted, "风险接受", 9},
	}
}

// All returns every known status value.
func All() []string {
	definitions := Definitions()
	values := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		values = append(values, definition.Value)
	}
	return values
}

// Valid reports whether value is a known finding status.
func Valid(value string) bool {
	for _, candidate := range All() {
		if candidate == value {
			return true
		}
	}
	return false
}

// Label returns the Chinese label for a status, falling back to the raw value
// so an unexpected stored value still renders instead of disappearing.
func Label(value string) string {
	for _, definition := range Definitions() {
		if definition.Value == value {
			return definition.Label
		}
	}
	return value
}

// closedStatuses are the statuses that mean "this finding no longer needs
// remediation work". resolved/ignored/duplicate are closed even though they
// were previously excluded from this set, which made dashboards report a
// finding as closed while its work order could never be completed.
func closedStatuses() []string {
	return []string{Resolved, Fixed, FalsePositive, Ignored, Duplicate, RiskAccepted}
}

// Closed reports whether a finding in this status is finished.
func Closed(value string) bool {
	for _, candidate := range closedStatuses() {
		if candidate == value {
			return true
		}
	}
	return false
}

// Open reports the inverse of Closed.
func Open(value string) bool {
	return Valid(value) && !Closed(value)
}

// ClosedSQL renders an `IN (...)` list for the closed statuses, for embedding
// directly in a query.
func ClosedSQL() string {
	quoted := make([]string, 0, len(closedStatuses()))
	for _, candidate := range closedStatuses() {
		quoted = append(quoted, "'"+candidate+"'")
	}
	return strings.Join(quoted, ", ")
}
