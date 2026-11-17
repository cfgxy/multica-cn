package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if got.Compactions[0].Source != "native" || got.Compactions[1].Source != "unknown" {
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
	if !ok || len(got.Compactions) != 1 || got.Compactions[0].Source != "unknown" {
		t.Fatalf("offset scan = %+v, ok=%v", got, ok)
	}
	got, ok = transcriptRunProvenance(path, int64(len(first)+len(second)+1))
	if !ok || len(got.Compactions) != 2 || got.StartOffset != 0 {
		t.Fatalf("stale offset scan = %+v, ok=%v", got, ok)
	}
}

func TestTranscriptRunProvenanceSourceDoesNotLeak(t *testing.T) {
	for _, source := range []string{"sensitive-example", strings.Repeat("secret", 20000)} {
		for _, key := range []string{"source", "summarySource", "summary_source"} {
			t.Run(key+"/"+source[:6], func(t *testing.T) {
				line, err := json.Marshal(map[string]any{"isCompactSummary": true, key: source})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "transcript.jsonl")
				if err := os.WriteFile(path, line, 0o600); err != nil {
					t.Fatal(err)
				}
				got, ok := transcriptRunProvenance(path, 0)
				if !ok || len(got.Compactions) != 1 || got.Compactions[0].Source != "unknown" {
					t.Fatalf("unsafe source classification: %+v, ok=%v", got, ok)
				}
				encoded, err := json.Marshal(got)
				if err != nil || strings.Contains(string(encoded), source) {
					t.Fatalf("source leaked in evidence")
				}
			})
		}
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
