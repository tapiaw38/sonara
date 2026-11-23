// Package strutil holds string helpers shared by the app and its packages.
package strutil

import (
	"strings"
	"unicode/utf8"
)

// Truncate trims value and cuts it to at most limit bytes on a rune boundary,
// marking the cut so a prompt never passes off partial text as complete.
func Truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "\n[truncated]"
}
