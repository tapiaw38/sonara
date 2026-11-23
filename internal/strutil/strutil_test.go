package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateContextPreservesUTF8(t *testing.T) {
	got := Truncate("áéíó", 3)
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "á\n") {
		t.Errorf("Truncate broke UTF-8: %q", got)
	}
}
