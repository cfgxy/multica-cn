package lark

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/pkg/redact"
)

// Tool-title and result-summary derivation for the progress card — the Go
// counterpart of the web transcript's readability layer
// (packages/core/task-transcript/trace-event-presenter.ts). The card renders
// one plain-text line per tool event, so unlike the web (CSS-truncated HTML
// rows) every derived string is collapsed to a single line here and clipped.
//
// Security posture: the only inputs are the payload's Input and Output — the
// channels the daemon already redacts (redact.InputMap) and truncates (8 KiB)
// and the server ingest re-redacts before publishing. The derivation still
// re-scrubs every extracted string with redact.Text as a third layer, so a
// daemon/server deployment-order skew upstream cannot turn the card into a
// secret channel. Nothing here opens a new data path.

// progressSummaryMaxRunes caps one derived title or result preview. Tool rows
// add label, dot and tool name on top of this clip, which keeps the whole row
// inside the per-entry envelope progressEntryMaxRunes guards for thinking
// text.
const progressSummaryMaxRunes = 120

// shellWrapperRe matches the login-shell invocations providers wrap real
// commands in (`<shell> -lc '<cmd>'`); the payload itself is matched manually
// because the quoted body needs a same-quote check RE2 cannot express as a
// backreference.
var shellWrapperRe = regexp.MustCompile(`^(?:/[\w./-]*/)?(?:zsh|bash|sh|fish)\s+(?:-[a-z]+\s+)*(.*)$`)

// progressToolTitle derives the one-line "what is this call doing" summary
// from a tool's input map. Preference order mirrors the web's
// traceToolArgSummary: the field a reviewer scans for first wins, falling
// back to the first short string value. An empty result renders the bare
// tool row.
func progressToolTitle(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	if s := firstString(input["query"]); s != "" {
		return presentOneLine(s)
	}
	if s := progressPatchSummary(input); s != "" {
		return presentOneLine(s)
	}
	if s := firstString(input["file_path"]); s != "" {
		return presentOneLine(shortenProgressPath(s))
	}
	if s := firstString(input["path"]); s != "" {
		return presentOneLine(shortenProgressPath(s))
	}
	if s := firstString(input["pattern"]); s != "" {
		return presentOneLine(s)
	}
	if s := firstString(input["description"]); s != "" {
		return presentOneLine(s)
	}
	if s := firstString(input["command"]); s != "" {
		return presentOneLine(stripShellWrapper(s))
	}
	if s := firstString(input["prompt"]); s != "" {
		return presentOneLine(s)
	}
	if s := firstString(input["skill"]); s != "" {
		return presentOneLine(s)
	}
	return presentOneLine(fallbackShortString(input))
}

// progressResultSummary derives the result preview from a tool's output.
// Output is persisted JSON-encoded, so exactly one string layer is unwrapped
// before the preview is collapsed and clipped — matching the web's
// traceEventSummary. An empty or blank output degrades to no summary.
func progressResultSummary(output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	return presentOneLine(unwrapToolOutput(output))
}

// presentOneLine is the uniform exit for every derived string: re-scrub,
// collapse whitespace so the markdown row stays one line, then clip to the
// row budget.
func presentOneLine(s string) string {
	return clipRunes(collapseWhitespace(redact.Text(s)), progressSummaryMaxRunes)
}

// progressPatchSummary names the first file of a multi-file patch input (the
// codex changes[] shape) plus a count of the rest, in the web's zh phrasing.
func progressPatchSummary(input map[string]any) string {
	changes, ok := input["changes"].([]any)
	if !ok {
		return ""
	}
	first := ""
	count := 0
	for _, c := range changes {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		if path == "" {
			continue
		}
		if count == 0 {
			first = path
		}
		count++
	}
	if count == 0 {
		return ""
	}
	head := shortenProgressPath(first)
	if count == 1 {
		return head
	}
	return fmt.Sprintf("%s，另有 %d 个文件", head, count-1)
}

// fallbackShortString scans the remaining values for the first string short
// enough to read as a summary. Keys are sorted so the pick is deterministic —
// Go map iteration order is not, and the card must not flicker between
// frames.
func fallbackShortString(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := input[k].(string); ok && s != "" && len([]rune(s)) < progressSummaryMaxRunes {
			return s
		}
	}
	return ""
}

func firstString(v any) string {
	s, _ := v.(string)
	return s
}

// stripShellWrapper unwraps a quoted login-shell invocation so the summary
// shows the real command. The payload must open and close with the same
// quote character; anything else is returned trimmed but untouched.
func stripShellWrapper(command string) string {
	trimmed := strings.TrimSpace(command)
	m := shellWrapperRe.FindStringSubmatch(trimmed)
	if m == nil {
		return trimmed
	}
	rest := m[1]
	if len(rest) >= 2 && (rest[0] == '\'' || rest[0] == '"') && rest[len(rest)-1] == rest[0] {
		return rest[1 : len(rest)-1]
	}
	return trimmed
}

// unwrapToolOutput decodes exactly one JSON string layer so the preview reads
// like the terminal output rather than its transport escaping. Anything that
// is not a wrapped string — a bare JSON document, plain prose, a body
// truncated mid-string by the 8 KiB channel cap — is returned untouched.
func unwrapToolOutput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return raw
	}
	var decoded string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return raw
	}
	return decoded
}

// collapseWhitespace turns multi-line content into the one line the card row
// can hold, the way the web's collapsed row never shows a newline.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// shortenProgressPath keeps only the tail of a deep path so the summary stays
// one line, mirroring the web's shortenTracePath.
func shortenProgressPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// clipRunes truncates by code points with the same ellipsis shape
// clampProgressText uses, so CJK text budgets by characters rather than
// bytes.
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
