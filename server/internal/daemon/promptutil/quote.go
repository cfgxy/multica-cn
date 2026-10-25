package promptutil

import (
	"strings"
)

// QuoteMarkdownLines keeps every input line inside a Markdown block quote.
func QuoteMarkdownLines(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u0085", "\n", string(rune(0x2028)), "\n", string(rune(0x2029)), "\n").Replace(text)
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
