package execenv

import (
	"strings"
	"unicode"
)

// SanitizePromptField strips anything that could break out of the surrounding
// prompt text when we interpolate a user- or agent-controlled value (display
// names, usernames) into the harness prompt or CLAUDE.md. Agent and member
// names are only validated as non-empty at write time, so raw values can
// contain newlines, backticks, or fake markdown that would otherwise turn
// the prompt into a cross-agent injection surface.
//
// The policy is intentionally conservative: drop all control runes, drop
// markdown structural characters (backtick, asterisk, brackets, pipe, angle
// brackets, hash, backslash), collapse internal whitespace, and truncate.
// The result stays inside the surrounding sentence and cannot introduce new
// mention links, code fences, or headings.
func SanitizePromptField(s string) string {
	const maxLen = 64
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastWasSpace := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if b.Len() > 0 && !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		switch r {
		case '`', '*', '_', '[', ']', '(', ')', '|', '>', '#', '\\':
			continue
		}
		b.WriteRune(r)
		lastWasSpace = false
		if b.Len() >= maxLen {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
