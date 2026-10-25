package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

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

func TestAuditableRuntimeBriefOmitsFailedFileInjection(t *testing.T) {
	const brief = "generated runtime brief"

	if got := auditableRuntimeBrief(brief, false, false, true); got != "" {
		t.Fatalf("failed file injection brief = %q, want omitted", got)
	}
	if got := auditableRuntimeBrief(brief, true, false, true); got != brief {
		t.Fatalf("successful file injection brief = %q, want %q", got, brief)
	}
	if got := auditableRuntimeBrief(brief, false, true, false); got != brief {
		t.Fatalf("failed inline injection brief = %q, want inline-delivered %q", got, brief)
	}
}

func TestFailedRuntimeConfigInjectionIsNotAuditedAsPromptInput(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(workDir, []byte("block directory creation"), 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}

	brief, err := execenv.InjectRuntimeConfig(workDir, "claude", execenv.TaskContextForEnv{IssueID: "issue-1"})
	if err == nil {
		t.Fatal("InjectRuntimeConfig() error = nil, want file write failure")
	}
	audit := buildTaskExecutionAudit("task-token", "", "prompt", auditableRuntimeBrief(brief, err == nil, false, true), "", "")
	for _, segment := range audit.PromptSegments {
		if segment.Source == "execenv.InjectRuntimeConfig" {
			t.Fatalf("failed file injection was included in prompt audit: %+v", segment)
		}
	}
}

func TestDeerFlowRuntimeBriefIsNotAuditedWithoutDelivery(t *testing.T) {
	workDir := t.TempDir()
	brief, err := execenv.InjectRuntimeConfig(workDir, "deerflow", execenv.TaskContextForEnv{IssueID: "issue-1"})
	if err != nil {
		t.Fatalf("InjectRuntimeConfig() error = %v, want nil", err)
	}
	if brief == "" {
		t.Fatal("InjectRuntimeConfig() brief is empty, want generated brief")
	}
	if providerNeedsInlineSystemPrompt("deerflow") {
		t.Fatal("DeerFlow unexpectedly requires inline system prompt")
	}
	runtimeConfigFile, readErr := execenv.ReadRuntimeConfigFile(workDir, "", "deerflow")
	if readErr != nil {
		t.Fatalf("ReadRuntimeConfigFile() error = %v, want nil", readErr)
	}
	if runtimeConfigFile != nil {
		t.Fatalf("DeerFlow runtime config file = %q, want no file target", runtimeConfigFile)
	}

	auditedBrief := auditableRuntimeBrief(brief, true, false, runtimeConfigFile != nil || readErr != nil)
	audit := buildTaskExecutionAudit("task-token", "", "prompt", auditedBrief, string(runtimeConfigFile), "")
	for _, segment := range audit.PromptSegments {
		if segment.Source == "execenv.InjectRuntimeConfig" {
			t.Fatalf("undelivered DeerFlow brief was included in prompt audit: %+v", segment)
		}
	}
}
