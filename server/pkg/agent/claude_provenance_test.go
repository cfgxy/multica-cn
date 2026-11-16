package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptRunProvenanceBoundsAndHashesCompactLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := "{\"type\":\"assistant\"}\n" +
		"{\"isCompactSummary\":true,\"summarySource\":\"native\",\"summary\":\"secret summary\"}\n" +
		"{\"isCompactSummary\":true,\"source\":\"fallback\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := transcriptRunProvenance(path, 0)
	if !ok {
		t.Fatal("expected provenance scan to succeed")
	}
	if got.StartOffset != 0 || got.EndOffset != int64(len(content)) {
		t.Fatalf("range = [%d,%d], want [0,%d]", got.StartOffset, got.EndOffset, len(content))
	}
	if len(got.SHA256) != 64 || len(got.Compactions) != 2 {
		t.Fatalf("hash/events = %q/%d, want a SHA-256 and two events", got.SHA256, len(got.Compactions))
	}
	if got.Compactions[0].Source != "native" || got.Compactions[1].Source != "fallback" {
		t.Fatalf("sources = %+v", got.Compactions)
	}
	if got.Compactions[0].SummarySHA256 == "secret summary" || len(got.Compactions[0].SummarySHA256) != 64 {
		t.Fatalf("summary hash leaked or was not hashed: %q", got.Compactions[0].SummarySHA256)
	}
}

func TestTranscriptRunProvenanceScopesToOffsetAndFallsBackAfterTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	first := "{\"isCompactSummary\":true}\n"
	second := "{\"isCompactSummary\":true,\"source\":\"second\"}\n"
	if err := os.WriteFile(path, []byte(first+second), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := transcriptRunProvenance(path, int64(len(first)))
	if !ok || len(got.Compactions) != 1 || got.Compactions[0].Source != "second" {
		t.Fatalf("offset scan = %+v, ok=%v", got, ok)
	}
	got, ok = transcriptRunProvenance(path, int64(len(first)+len(second)+1))
	if !ok || len(got.Compactions) != 2 || got.StartOffset != 0 {
		t.Fatalf("stale offset scan = %+v, ok=%v", got, ok)
	}
}

func TestTranscriptRunProvenanceKeepsFinalLineOffsetWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := "{\"isCompactSummary\":true,\"source\":\"final\"}"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := transcriptRunProvenance(path, 0)
	if !ok || got.EndOffset != int64(len(content)) || len(got.Compactions) != 1 {
		t.Fatalf("final-line provenance = %+v, ok=%v", got, ok)
	}
}
