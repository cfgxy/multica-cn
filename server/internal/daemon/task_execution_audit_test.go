package daemon

import "testing"

func TestBuildTaskExecutionAuditUsesKeyedDigestsAndOnlyMetadata(t *testing.T) {
	priorSessionID := "sensitive-session-id"
	prompt := "sensitive prompt body"
	runtimeConfig := "runtime config body"
	runtimeConfigFile := "user instructions\nruntime config body"
	inlineSystemPrompt := "inline system body"
	audit := buildTaskExecutionAudit("task-token", priorSessionID, prompt, runtimeConfig, runtimeConfigFile, inlineSystemPrompt)

	if audit.Version != 1 || audit.ResumeOutcome != "unknown_or_failed" {
		t.Fatalf("unexpected audit header: %+v", audit)
	}
	if audit.PriorSessionDigest == "" || audit.PriorSessionDigest == priorSessionID {
		t.Fatalf("prior session ID was not digested: %+v", audit)
	}
	if len(audit.PromptSegments) != 4 || audit.PromptSegments[0].LengthBytes != len(prompt) || audit.PromptSegments[1].LengthBytes != len(runtimeConfig) || audit.PromptSegments[2].LengthBytes != len(runtimeConfigFile) {
		t.Fatalf("unexpected prompt segments: %+v", audit.PromptSegments)
	}
	if audit.SessionSelectorCounts["resume"] != 1 || audit.FinalPromptDigest == "" {
		t.Fatalf("missing selection or final digest: %+v", audit)
	}
	for _, value := range []string{priorSessionID, prompt, runtimeConfig, runtimeConfigFile, inlineSystemPrompt} {
		for _, segment := range audit.PromptSegments {
			if segment.Digest == value {
				t.Fatalf("audit contains raw content: %+v", audit)
			}
		}
	}
	if audit.PriorSessionDigest == buildTaskExecutionAudit("different-key", priorSessionID, prompt, runtimeConfig, runtimeConfigFile, inlineSystemPrompt).PriorSessionDigest {
		t.Fatal("session digest must be keyed")
	}
}

func TestBuildTaskExecutionAuditWithoutPriorSession(t *testing.T) {
	audit := buildTaskExecutionAudit("task-token", "", "prompt", "runtime config", "runtime config file", "")
	if audit.ResumeOutcome != "not_requested" || audit.PriorSessionDigest != "" {
		t.Fatalf("unexpected fresh-session audit: %+v", audit)
	}
	if audit.SessionSelectorCounts["resume"] != 0 || len(audit.PromptSegments) != 3 {
		t.Fatalf("unexpected fresh-session selectors or prompt segments: %+v", audit)
	}
}
