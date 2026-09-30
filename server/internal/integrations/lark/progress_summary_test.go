package lark

import (
	"strings"
	"testing"
)

// TestProgressToolTitle pins the title derivation: the preference order
// mirrors the web transcript's traceToolArgSummary, every derived string is
// collapsed to one line, re-scrubbed with redact.Text and clipped.
func TestProgressToolTitle(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"nil input degrades to no title", nil, ""},
		{"empty input degrades to no title", map[string]any{}, ""},
		{"command passes through", map[string]any{"command": "git status"}, "git status"},
		{"single-quoted shell wrapper stripped", map[string]any{"command": "bash -lc 'git status'"}, "git status"},
		{"shell path and stacked flags stripped", map[string]any{"command": "/bin/zsh -x -lc \"echo hi\""}, "echo hi"},
		{"multi-line command collapses to one line", map[string]any{"command": "echo a\necho b"}, "echo a echo b"},
		{"query wins over command", map[string]any{"query": "progress card", "command": "git status"}, "progress card"},
		{"long file path shortened", map[string]any{"file_path": "/a/b/c/d/e.txt"}, ".../d/e.txt"},
		{"short path key kept verbatim", map[string]any{"path": "/a/b.txt"}, "/a/b.txt"},
		{"pattern verbatim", map[string]any{"pattern": "progressEntry.*"}, "progressEntry.*"},
		{"description verbatim", map[string]any{"description": "读取配置"}, "读取配置"},
		{
			"prompt clipped to the row budget",
			map[string]any{"prompt": strings.Repeat("问", progressSummaryMaxRunes+5)},
			strings.Repeat("问", progressSummaryMaxRunes) + "…",
		},
		{"skill verbatim", map[string]any{"skill": "collab-handbook"}, "collab-handbook"},
		{"non-string preferred key falls through", map[string]any{"query": 42, "skill": "kb"}, "kb"},
		{
			"multi-file patch names first file and count",
			map[string]any{"changes": []any{
				map[string]any{"path": "/x/y/z/w/a.go"},
				map[string]any{"path": "b.go"},
				map[string]any{"path": "c.go"},
			}},
			".../w/a.go，另有 2 个文件",
		},
		{
			"single-file patch names the file",
			map[string]any{"changes": []any{map[string]any{"path": "/x/a.go"}}},
			"/x/a.go",
		},
		{"fallback picks first string by sorted key", map[string]any{"zebra": "last", "alpha": "first"}, "first"},
		{"fallback skips overlong strings", map[string]any{"big": strings.Repeat("x", 500), "small": "ok"}, "ok"},
		{"fallback ignores non-strings", map[string]any{"n": 7, "m": map[string]any{"k": "v"}}, ""},
		{
			"raw credential re-scrubbed at the card layer",
			map[string]any{"command": "curl -H 'Authorization: Bearer supersecretvalue'"},
			"curl -H 'Authorization: Bearer [REDACTED]'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressToolTitle(tc.input); got != tc.want {
				t.Fatalf("progressToolTitle(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestProgressResultSummary pins the result preview: one JSON string layer is
// unwrapped, whitespace runs collapse so the row stays one line, and the
// result is re-scrubbed and clipped. Empty or blank output degrades to no
// summary rather than rendering an empty preview.
func TestProgressResultSummary(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{"empty output degrades", "", ""},
		{"whitespace-only output degrades", "  \n\t ", ""},
		{"plain output verbatim", "done in 2s", "done in 2s"},
		{"multi-line output collapses", "line one\nline two", "line one line two"},
		{"json-encoded string unwraps", `"hello\nworld"`, "hello world"},
		{"json-encoded escaped quotes unwrap", `"say \"hi\""`, `say "hi"`},
		{"json-encoded multi-line unwraps", `"line a\nline b"`, "line a line b"},
		{
			"overlong output clips to the row budget",
			strings.Repeat("x", progressSummaryMaxRunes+5),
			strings.Repeat("x", progressSummaryMaxRunes) + "…",
		},
		{"json object output untouched", `{"ok":true}`, `{"ok":true}`},
		{"truncated json string stays raw", `"unterminated`, `"unterminated`},
		{"credential in output re-scrubbed", "Authorization: Bearer supersecretvalue", "Authorization: Bearer [REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressResultSummary(tc.output); got != tc.want {
				t.Fatalf("progressResultSummary(%q) = %q, want %q", tc.output, got, tc.want)
			}
		})
	}
}

// TestStripShellWrapper pins the wrapper forms the derivation unwraps;
// anything that is not a quoted shell invocation stays untouched.
func TestStripShellWrapper(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"plain command untouched", "git status", "git status"},
		{"single-quoted wrapper stripped", "bash -lc 'git status'", "git status"},
		{"double-quoted wrapper stripped", `sh -c "echo hi"`, "echo hi"},
		{"shell path and stacked flags", "/bin/zsh -x -lc 'echo hi'", "echo hi"},
		{"inner quotes preserved", `bash -lc "echo 'quoted'"`, `echo 'quoted'`},
		{"unquoted payload not stripped", "bash -lc echo hi", "bash -lc echo hi"},
		{"non-shell prefix untouched", "timeout 5 git status", "timeout 5 git status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripShellWrapper(tc.command); got != tc.want {
				t.Fatalf("stripShellWrapper(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

// TestShortenProgressPath pins the .../parent/leaf form the web uses so a
// deep path keeps the tool summary on one line.
func TestShortenProgressPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"short path unchanged", "a/b/c", "a/b/c"},
		{"deep path shortened", "/a/b/c/d/e.txt", ".../d/e.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortenProgressPath(tc.path); got != tc.want {
				t.Fatalf("shortenProgressPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestClipRunes pins the clip idiom: same shape as clampProgressText, budget
// counted in runes so CJK text truncates by characters, not bytes.
func TestClipRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under budget unchanged", "hello", 10, "hello"},
		{"exact budget unchanged", "hello", 5, "hello"},
		{"over budget clips with ellipsis", "hello", 4, "hell…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clipRunes(tc.in, tc.max); got != tc.want {
				t.Fatalf("clipRunes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}
