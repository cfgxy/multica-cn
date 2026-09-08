package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPriorRunResultText(t *testing.T) {
	t.Parallel()

	output, err := json.Marshal(TaskCompleteRequest{Output: "  shipped the fix  ", SessionID: "s1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tests := []struct {
		name   string
		result []byte
		want   string
	}{
		{name: "empty blob", result: nil, want: ""},
		// A malformed blob must not take the claim path down: an unreadable
		// prior result costs the brief one section, not the run.
		{name: "malformed blob", result: []byte("not json"), want: ""},
		{name: "no output field", result: []byte(`{"session_id":"s1"}`), want: ""},
		{name: "output is trimmed", result: output, want: "shipped the fix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := priorRunResultText(tt.result); got != tt.want {
				t.Fatalf("priorRunResultText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClipForBrief(t *testing.T) {
	t.Parallel()

	if got := clipForBrief("  short  ", 100); got != "short" {
		t.Fatalf("under the limit must pass through trimmed, got %q", got)
	}

	long := strings.Repeat("a", 200)
	got := clipForBrief(long, 50)
	if !strings.HasSuffix(got, "… [truncated]") {
		t.Fatalf("clipped text must be marked as clipped, got %q", got)
	}
	if len(got) > 50+len("… [truncated]") {
		t.Fatalf("clipped text overshot its budget: %d bytes", len(got))
	}

	// Multi-byte input is the case a naive byte slice corrupts: a prompt
	// carrying invalid UTF-8 is a worse failure than a slightly shorter clip.
	cjk := strings.Repeat("会话压缩", 100)
	for _, limit := range []int{1, 5, 7, 64, 301} {
		clipped := clipForBrief(cjk, limit)
		if !utf8.ValidString(clipped) {
			t.Fatalf("clipForBrief(cjk, %d) produced invalid UTF-8: %q", limit, clipped)
		}
	}
}

func TestBriefCommentLineIsAnAnchorNotContent(t *testing.T) {
	t.Parallel()

	row := db.ListRootCommentsForIssueRow{
		ID:             parseUUID("11111111-2222-3333-4444-555555555555"),
		AuthorType:     "member",
		Content:        strings.Repeat("x", priorContextBriefCommentBytes*3),
		ReplyCount:     7,
		LastActivityAt: pgtype.Timestamptz{Valid: false},
	}

	line := briefCommentLine(row, "Bohan")
	if !strings.Contains(line, "`11111111`") {
		t.Errorf("line must carry a short comment id for Ctrl+F, got %q", line)
	}
	if !strings.Contains(line, "Bohan") || !strings.Contains(line, "7 replies") {
		t.Errorf("line must carry author and reply count, got %q", line)
	}
	// The excerpt is a pointer into the issue, not a copy of it — re-injecting
	// whole comment bodies would rebuild the context the gate just shed.
	if len(line) > priorContextBriefCommentBytes*2 {
		t.Errorf("line ignored the per-comment budget: %d bytes", len(line))
	}

	// Falling back to the author TYPE keeps the line readable when the name
	// lookup fails, instead of rendering an anonymous bullet.
	if line := briefCommentLine(row, ""); !strings.Contains(line, "member") {
		t.Errorf("missing author name must fall back to the author type, got %q", line)
	}
}
