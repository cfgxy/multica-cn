package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// runStatsFixture writes raw JSONL lines to a temp file and returns its path
// — the shape transcriptRunStats scans. Distinct from the package's
// writeTranscript (claude_transcript_test.go), which lays out a session
// directory tree for transcriptContextReading's lookup-by-session-id path;
// transcriptRunStats is handed a path directly and needs no such layout.
func runStatsFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// The acceptance fixture for RUYI-154: a run with two native auto-compacts and
// three assistant requests of rising size must report the LARGEST single
// request's context size, not the sum of all three, and must count exactly
// two compactions — one per isCompactSummary boundary entry.
func TestTranscriptRunStatsMaxContextAndCompactionCount(t *testing.T) {
	t.Parallel()

	path := runStatsFixture(t,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}`,
		`{"isCompactSummary":true,"type":"user"}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-opus-5","usage":{"input_tokens":200,"output_tokens":20,"cache_read_input_tokens":2000,"cache_creation_input_tokens":0}}}`,
		`{"isCompactSummary":true,"type":"user"}`,
		`{"type":"assistant","message":{"id":"m3","model":"claude-opus-5","usage":{"input_tokens":300,"output_tokens":30,"cache_read_input_tokens":9000,"cache_creation_input_tokens":0}}}`,
	)

	maxCtx, compactions, ok := transcriptRunStats(path)
	if !ok {
		t.Fatal("transcriptRunStats reported no usable reading")
	}
	// Largest single request: m3 = 300 + 9000 + 0 + 30 = 9330.
	if want := int64(9_330); maxCtx != want {
		t.Errorf("maxCtx = %d, want %d (the largest single request, not the sum)", maxCtx, want)
	}
	if compactions != 2 {
		t.Errorf("compactions = %d, want 2", compactions)
	}
}

// A run with no isCompactSummary boundary anywhere in its transcript made
// zero native auto-compacts — the count must be a real 0, since the run DID
// produce usable readings and simply never compacted.
func TestTranscriptRunStatsZeroCompactionsWhenNoneOccurred(t *testing.T) {
	t.Parallel()

	path := runStatsFixture(t,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	)

	maxCtx, compactions, ok := transcriptRunStats(path)
	if !ok {
		t.Fatal("transcriptRunStats reported no usable reading")
	}
	if maxCtx != 110 {
		t.Errorf("maxCtx = %d, want 110", maxCtx)
	}
	if compactions != 0 {
		t.Errorf("compactions = %d, want 0", compactions)
	}
}

// Sidechain (subagent) traffic must not contribute to this session's max
// context reading — it runs in a parallel conversation with its own budget.
func TestTranscriptRunStatsSkipsSidechainLines(t *testing.T) {
	t.Parallel()

	path := runStatsFixture(t,
		`{"type":"assistant","isSidechain":true,"message":{"id":"sub1","model":"claude-opus-5","usage":{"input_tokens":999999,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	)

	maxCtx, _, ok := transcriptRunStats(path)
	if !ok {
		t.Fatal("transcriptRunStats reported no usable reading")
	}
	if maxCtx != 110 {
		t.Errorf("maxCtx = %d, want 110 — the sidechain's huge reading must not count", maxCtx)
	}
}

// A missing or unreadable transcript file must report ok=false so the caller
// leaves Turns/Compactions/MaxContextTokens at zero rather than a fabricated
// reading.
func TestTranscriptRunStatsMissingFile(t *testing.T) {
	t.Parallel()

	_, _, ok := transcriptRunStats(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if ok {
		t.Error("expected ok=false for a missing transcript file")
	}
}
