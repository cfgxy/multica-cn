package handler

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestTaskToResponseExposesValidExecutionAudit(t *testing.T) {
	result, err := json.Marshal(map[string]any{
		"status":     "completed",
		"session_id": "private-session-id",
		"execution_audit": map[string]any{
			"version":                 1,
			"resume_outcome":          "rejected_new_session",
			"prior_session_digest":    "aabbcc0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			"session_selector_counts": map[string]int{"resume": 1},
			"prompt_segments": []map[string]any{{
				"source": "daemon.BuildPrompt", "version": "1", "length_bytes": 10, "digest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			}},
			"final_prompt_digest": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := taskToResponse(db.AgentTaskQueue{Result: result}, "workspace")
	if response.ExecutionAudit == nil || response.ExecutionAudit.ResumeOutcome != "rejected_new_session" {
		t.Fatalf("execution audit = %+v", response.ExecutionAudit)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-session-id") {
		t.Fatal("public task response exposed the raw session ID")
	}
}

func TestTaskToResponseOmitsMalformedOrLegacyExecutionAudit(t *testing.T) {
	for _, result := range [][]byte{
		[]byte(`{"status":"completed"}`),
		[]byte(`{"execution_audit":{"version":9,"resume_outcome":"accepted"}}`),
		[]byte(`{"execution_audit":{"version":1,"resume_outcome":"invented","final_prompt_digest":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"}}`),
	} {
		response := taskToResponse(db.AgentTaskQueue{Result: result}, "workspace")
		if response.ExecutionAudit != nil {
			t.Fatalf("malformed/legacy audit was exposed: %+v", response.ExecutionAudit)
		}
	}
}
