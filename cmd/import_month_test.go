package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// goWords is a run of three English words: what the text of Go's own parser is made of,
// and what nothing the user is told in Chinese should carry.
var goWords = regexp.MustCompile(`[A-Za-z]+( [A-Za-z]+){2,}`)

func TestAMonthThatIsNotOneIsRefusedInChineseWithTheFormatAndNoTextOfGo(t *testing.T) {
	for _, month := range []string{"abc", "2025-13", "2025", "2025/03", "202503", "2025-3", " "} {
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"import", "moze", "confirm", "--month", month})
		err := root.Execute()
		if err == nil {
			t.Errorf("--month %q was accepted", month)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "YYYY-MM") || !strings.Contains(msg, "2025-03") || !strings.Contains(msg, "要寫成") {
			t.Errorf("--month %q = %q, want it to say the format and an example in Chinese", month, msg)
		}
		for _, goText := range []string{"parsing time", "cannot parse", "out of range", "extra text", `"2006`} {
			if strings.Contains(msg, goText) {
				t.Errorf("--month %q = %q, want none of Go's own text (%q)", month, msg, goText)
			}
		}
		if goWords.MatchString(msg) {
			t.Errorf("--month %q = %q, want no run of English words", month, msg)
		}
	}
}
