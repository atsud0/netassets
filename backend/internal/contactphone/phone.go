// Package contactphone holds the one place where the product decides what a
// "phone number" field may contain.
//
// Contact numbers are captured in many places: business-system owner contact,
// organization contact, vendor contact and user mobile/office phone. Each of
// those screens used to accept arbitrary text, so typos and obviously wrong
// values (a five digit fragment, a department name, "abc") silently entered the
// ledger and were later useless for contacting the owner. Sharing one validator
// keeps the rule identical everywhere and makes it testable in isolation.
package contactphone

import (
	"regexp"
	"strings"
)

const MaxLength = 64

// Pattern accepts the shapes actually used in Chinese group enterprises:
// mainland mobile numbers, landlines with an optional area code and extension,
// 400/800 service numbers, and a short internal extension. Separators (space,
// dash, parentheses) and an optional country prefix are tolerated because real
// ledgers carry them.
var pattern = regexp.MustCompile(`^(?:\+?86[- ]?)?(?:` +
	`1[3-9]\d{9}` + // mainland mobile
	`|0\d{2,3}[- ]?\d{7,8}(?:[- ]?\d{1,6})?` + // landline with area code
	`|[48]00[- ]?\d{3}[- ]?\d{4}` + // 400 / 800 service number
	`|\d{3,6}` + // internal extension
	`)$`)

var loosePattern = regexp.MustCompile(`^[0-9+\-()\s]+$`)

// IsValid reports whether value is an acceptable contact number. An empty value
// means "not provided" and is always valid; callers decide separately whether the
// field is required.
func IsValid(value string) bool {
	normalized := Normalize(value)
	if normalized == "" {
		return true
	}
	return pattern.MatchString(normalized)
}

// Normalize trims the value and removes the separators people type by habit so
// that "138-0000-0000" and "13800000000" compare and store consistently.
func Normalize(value string) string {
	compact := strings.TrimSpace(value)
	if compact == "" {
		return ""
	}
	if !loosePattern.MatchString(compact) {
		return strings.TrimSpace(value)
	}
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	return replacer.Replace(compact)
}
