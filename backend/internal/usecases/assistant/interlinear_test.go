package assistant

import "testing"

func TestParseAlignedTranslation(t *testing.T) {
	const original = "¿Cómo lo hiciste?\n¿Qué cambió?"
	valid := `[{"original":"¿Cómo lo hiciste?","translation":"How did you do it?"},{"original":"¿Qué cambió?","translation":"What changed?"}]`
	for _, tc := range []struct {
		name, response string
		ok             bool
	}{
		{"paired", valid, true},
		{"fenced JSON", "```json\n" + valid + "\n```", true},
		{"missing pair", `[{"original":"¿Cómo lo hiciste?","translation":"How did you do it?"}]`, false},
		{"reordered", `[{"original":"¿Qué cambió?","translation":"What changed?"},{"original":"¿Cómo lo hiciste?","translation":"How did you do it?"}]`, false},
		{"changed source", `[{"original":"¿Cómo lo hizo?","translation":"How did you do it?"},{"original":"¿Qué cambió?","translation":"What changed?"}]`, false},
		{"empty translation", `[{"original":"¿Cómo lo hiciste?","translation":""},{"original":"¿Qué cambió?","translation":"What changed?"}]`, false},
		{"split word", `[{"original":"¿Có","translation":"How"},{"original":"mo lo hiciste?","translation":"did you do it?"},{"original":"¿Qué cambió?","translation":"What changed?"}]`, false},
		{"plain text", "How did you do it? What changed?", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := parseAlignedTranslation(original, tc.response)
			if tc.ok {
				if err != nil || len(lines) != 2 || lines[1].Translation != "What changed?" {
					t.Fatalf("lines=%+v err=%v", lines, err)
				}
			} else if err == nil || len(lines) != 0 {
				t.Fatalf("accepted invalid alignment: %+v", lines)
			}
		})
	}
}
