package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

func TestParseRunActivityCursorAndLimit(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      int32
		wantSet   bool
		wantError bool
	}{
		{name: "omitted cursor"},
		{name: "zero cursor", input: "0", wantSet: true},
		{name: "positive cursor", input: "17", want: 17, wantSet: true},
		{name: "negative cursor", input: "-1", wantError: true},
		{name: "overflow cursor", input: "2147483648", wantError: true},
		{name: "fractional cursor", input: "1.5", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, set, err := parseRunActivityAfterSeq(tt.input)
			if (err != nil) != tt.wantError || got != tt.want || set != tt.wantSet {
				t.Fatalf("parseRunActivityAfterSeq(%q) = (%d, %t, %v)", tt.input, got, set, err)
			}
		})
	}

	for _, tt := range []struct {
		input string
		want  int
		bad   bool
	}{{"", runActivityDefaultLimit, false}, {"1", 1, false}, {"100", 100, false}, {"0", 0, true}, {"101", 0, true}, {"x", 0, true}} {
		got, err := parseRunActivityLimit(tt.input)
		if (err != nil) != tt.bad || got != tt.want {
			t.Fatalf("parseRunActivityLimit(%q) = (%d, %v)", tt.input, got, err)
		}
	}
}

func TestRunActivityProgressSeparatesSnapshotAndNewEvidence(t *testing.T) {
	created := pgtype.Timestamptz{Time: time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC), Valid: true}
	latest := db.ListTaskMessagesPageRow{Seq: 8, CreatedAt: created}
	newMessage := db.ListTaskMessagesPageRow{Seq: 9, CreatedAt: created}
	baseTask := db.AgentTaskQueue{Status: "running", CreatedAt: created}

	tests := []struct {
		name      string
		task      db.AgentTaskQueue
		rows      []db.ListTaskMessagesPageRow
		last      pgtype.Timestamptz
		hasLatest bool
		cursor    bool
		after     int32
		want      string
	}{
		{name: "first snapshot does not imply progress", task: baseTask, rows: []db.ListTaskMessagesPageRow{latest}, last: created, hasLatest: true, want: "no_recent_evidence"},
		{name: "new sequence is progress", task: baseTask, rows: []db.ListTaskMessagesPageRow{newMessage}, last: created, hasLatest: true, cursor: true, after: 8, want: "advancing"},
		{name: "running without any trace is unknown", task: baseTask, want: "unknown"},
		{name: "terminal status wins", task: db.AgentTaskQueue{Status: "completed"}, want: "finished"},
		{name: "explicit wait wins", task: db.AgentTaskQueue{Status: "running", WaitReason: pgtype.Text{String: "waiting for worker", Valid: true}}, want: "waiting"},
		{name: "waiting state is explicit", task: db.AgentTaskQueue{Status: "waiting_local_directory"}, want: "waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunActivityResponse(tt.task, tt.rows, tt.last, tt.hasLatest, tt.after, tt.cursor, false)
			if got.ProgressAssessment.Status != tt.want {
				t.Fatalf("assessment = %q, want %q", got.ProgressAssessment.Status, tt.want)
			}
			if got.WorkerEvidence.ProcessLiveness != "unknown" || got.RunState.Status == "running" && got.ProgressAssessment.Status == "advancing" && !tt.cursor {
				t.Fatalf("worker/status evidence was conflated: %+v", got)
			}
		})
	}
}

func TestRunActivityMessageIsRedactedAndBounded(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	message := boundRunActivityMessage(runActivityMessage{
		Seq: 1, Type: "tool_result", Content: redact.Text(strings.Repeat("x", 9000) + " " + secret),
		Output: strings.Repeat("y", 9000),
		Input:  redact.InputMap(map[string]any{"token": secret}),
	})
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > runActivityMessageBytes {
		t.Fatalf("encoded message is %d bytes, limit is %d", len(encoded), runActivityMessageBytes)
	}
	if !message.Truncated || strings.Contains(string(encoded), secret) {
		t.Fatalf("message not truncated/redacted: %s", encoded)
	}
}
