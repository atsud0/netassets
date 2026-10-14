// Package textlength is the one place where the product decides how long a
// free-text field may be.
//
// The reported defect was a family of "输入过程报错" bugs: 填报任务的"任务说明"
// accepts 5000 characters in the browser but the API rejects the request long
// before that. The cause was that every service measured the input with Go's
// len(), which counts BYTES, while the database and the browser both count
// CHARACTERS. A single Chinese glyph is three bytes in UTF-8, so a field the UI
// advertises as "最多 5000 字" started failing at about 1666 字 - and a field
// stored in a varchar(255) column failed at 85 字.
//
// Using bytes here is wrong twice over: it rejects text that fits, and it makes
// the accepted length depend on which language the reviewer happens to type in.
// Postgres varchar(n) counts characters, so this package counts characters too
// and every limit is expressed in the same unit the user sees.
package textlength

import "unicode/utf8"

// Count returns the number of characters (runes) in value. Invalid UTF-8 bytes
// count as one character each so a truncated request can never pass a limit.
func Count(value string) int {
	return utf8.RuneCountInString(value)
}

// Exceeds reports whether value is longer than the limit, which is how the
// services phrase the check ("... > N" means invalid).
func Exceeds(value string, limit int) bool {
	return Count(value) > limit
}

// Within reports whether value fits the limit.
func Within(value string, limit int) bool {
	return !Exceeds(value, limit)
}

// Shorter reports whether value is below a minimum length. Passwords and other
// ASCII-only secrets use it so the rule reads the same way as Exceeds.
func Shorter(value string, limit int) bool {
	return Count(value) < limit
}
