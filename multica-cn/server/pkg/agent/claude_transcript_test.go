package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// transcriptAssistantLine renders one transcript JSONL line the way the CLI
// writes it. usage mirrors the real numbers; the stream-json shape the CLI
// emits (input/output zeroed) is what msgZeroed produces.
func transcriptAssistantLine(t *testing.T, id, model string, input, cacheRead, cacheWrite, output int64, sidechain bool) []byte {
	t.Helper()
	rec := map[string]any{
		"type":        "assistant",
		"isSidechain": sidechain,
		"sessionId":   "sid",
		"message": map[string]any{
			"id":    id,
			"role":  "assistant",
			"model": model,
			"usage": map[string]any{
				"input_tokens":                input,
				"output_tokens":               output,
				"cache_read_input_tokens":     cacheRead,
				"cache_creation_input_tokens": cacheWrite,
			},
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// writeTranscript lays out <root>/<slug-of-cwd>/<sessionID>.jsonl.
func writeTranscript(t *testing.T, root, cwd, sessionID string, lines ...[]byte) {
	t.Helper()
	dir := filepath.Join(root, claudeProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The reading must be the LAST request's input side plus its own output — the
// statistic "what the next resume inherits" — not the run's billing sum, and
// not the first request. One request emits several lines sharing a message id
// (one per content block); the duplicates must collapse, or the last request
// would be misread as the sum of its own blocks. The real implementation call
// lives in TestTranscriptContextReadingValues below; last request input side
// 16814+3584 plus its own output 15.
func TestTranscriptContextReadingValues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd := "/home/guxy/multica_workspaces/task/workdir"
	sid := "sid-1"
	writeTranscript(t, root, cwd, sid,
		transcriptAssistantLine(t, "msg_1", "gpt-5.6-terra", 10_000, 2_000, 0, 100, false),
		transcriptAssistantLine(t, "msg_1", "gpt-5.6-terra", 10_000, 2_000, 0, 100, false),
		transcriptAssistantLine(t, "msg_2", "gpt-5.6-terra", 0, 0, 0, 0, false),
		transcriptAssistantLine(t, "msg_3", "gpt-5.6-terra", 16_814, 3_584, 0, 15, false),
	)

	path, found := claudeTranscriptPath(root, cwd, sid)
	if !found {
		t.Fatal("transcript not found via slug")
	}
	model, tokens, ok := transcriptContextReading(path)
	if !ok {
		t.Fatal("no reading extracted from a well-formed transcript")
	}
	if model != "gpt-5.6-terra" {
		t.Errorf("model = %q, want gpt-5.6-terra (the LAST request's model)", model)
	}
	if want := int64(16_814 + 3_584 + 15); tokens != want {
		t.Errorf("tokens = %d, want %d (last request input side + its own output)", tokens, want)
	}
}

// Subagent traffic must not count toward the session's size: a sidechain line
// that is both newer and larger than the last real request is skipped, and a
// transcript containing nothing but sidechain traffic yields no reading.
func TestTranscriptContextReadingSkipsSidechain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd := "/work"
	sid := "sid-side"
	writeTranscript(t, root, cwd, sid,
		transcriptAssistantLine(t, "msg_1", "gpt-5.6-terra", 5_000, 0, 0, 10, false),
		transcriptAssistantLine(t, "msg_sub", "gpt-5.6-terra", 900_000, 0, 0, 0, true),
	)

	path, found := claudeTranscriptPath(root, cwd, sid)
	if !found {
		t.Fatal("transcript not found")
	}
	model, tokens, ok := transcriptContextReading(path)
	if !ok || model != "gpt-5.6-terra" || tokens != 5_010 {
		t.Fatalf("got model=%q tokens=%d ok=%v, want the non-sidechain request only", model, tokens, ok)
	}

	root2 := t.TempDir()
	writeTranscript(t, root2, cwd, sid,
		transcriptAssistantLine(t, "msg_sub", "gpt-5.6-terra", 900_000, 0, 0, 0, true),
	)
	path2, _ := claudeTranscriptPath(root2, cwd, sid)
	if _, _, ok := transcriptContextReading(path2); ok {
		t.Error("a sidechain-only transcript must produce no reading")
	}
}

// A missing transcript, an empty usage map, or a session id that locates no
// file must all be silent no-ops: the gate then reads size_unknown and
// resumes, which is the safe direction (D4 A).
func TestFillContextTokensFromTranscriptDegradesSilently(t *testing.T) {
	t.Parallel()

	usage := map[string]TokenUsage{"gpt-5.6-terra[1m]": {InputTokens: 100}}
	before := usage["gpt-5.6-terra[1m]"]

	// No transcript anywhere under root.
	fillContextTokensFromTranscript(usage, t.TempDir(), "/work", "no-such-session", slog.Default())
	if usage["gpt-5.6-terra[1m]"].ContextTokens != 0 {
		t.Error("missing transcript must leave the reading empty")
	}

	// Empty usage map and empty session id: no-op by contract.
	fillContextTokensFromTranscript(map[string]TokenUsage{}, t.TempDir(), "/work", "sid", slog.Default())
	fillContextTokensFromTranscript(usage, t.TempDir(), "/work", "", slog.Default())
	if usage["gpt-5.6-terra[1m]"] != before {
		t.Error("no-op paths must not touch the usage map")
	}
}

// The end-to-end fill: stream events carried nothing (usage zeroed by the
// CLI), so the transcript reading lands on the totals entry spelled with the
// [1m] variant suffix via normalizeModelKey — and a stream-provided reading
// on any entry is never overridden.
func TestFillContextTokensFromTranscriptFillsDriftedKeyAndDefersToStream(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd := "/work/dir"
	sid := "sid-fill"
	writeTranscript(t, root, cwd, sid,
		transcriptAssistantLine(t, "msg_1", "gpt-5.6-terra", 300_000, 20_000, 0, 1_234, false),
	)

	usage := map[string]TokenUsage{
		"gpt-5.6-terra[1m]": {InputTokens: 100, OutputTokens: 10},
		"gpt-5.6-sol[1m]":   {InputTokens: 50, OutputTokens: 5, ContextTokens: 777},
	}
	fillContextTokensFromTranscript(usage, root, cwd, sid, slog.Default())

	if got := usage["gpt-5.6-terra[1m]"].ContextTokens; got != 321_234 {
		t.Errorf("context tokens = %d, want 321234 (300000+20000+1234 folded onto the [1m] totals entry)", got)
	}
	if got := usage["gpt-5.6-sol[1m]"].ContextTokens; got != 777 {
		t.Errorf("stream reading was overridden: %d, want 777", got)
	}
}

// The transcript model may match no totals entry at all (the request that
// produced the reading belonged to a model the result totals dropped); then
// nothing is filled — borrowing another model's context size would misgate a
// session the gate knows nothing about.
func TestFillContextTokensFromTranscriptNeverBorrows(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTranscript(t, root, "/work", "sid-nb",
		transcriptAssistantLine(t, "msg_1", "model-nowhere", 400_000, 0, 0, 0, false),
	)
	usage := map[string]TokenUsage{"gpt-5.6-terra[1m]": {InputTokens: 100}}
	fillContextTokensFromTranscript(usage, root, "/work", "sid-nb", slog.Default())
	if got := usage["gpt-5.6-terra[1m]"].ContextTokens; got != 0 {
		t.Errorf("reading borrowed across models: %d, want 0", got)
	}
}

// claudeProjectSlug must reproduce the CLI's directory naming: everything
// outside [A-Za-z0-9-] becomes a dash, verified against the live CLI for
// `/tmp/gh.audit.slug.test` -> `-tmp-gh-audit-slug-test`.
func TestClaudeProjectSlug(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"/tmp", "-tmp"},
		{"/tmp/gh.audit.slug.test", "-tmp-gh-audit-slug-test"},
		{"/home/guxy/multica_workspaces/shanghui-4da3341f7ba8/shan-224-09a3c39122e7/worktree",
			"-home-guxy-multica-workspaces-shanghui-4da3341f7ba8-shan-224-09a3c39122e7-worktree"},
	}
	for _, tt := range tests {
		if got := claudeProjectSlug(tt.in); got != tt.want {
			t.Errorf("claudeProjectSlug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
