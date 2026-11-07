package knowledgegovernance

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testWorkspaceID = "00000000-0000-0000-0000-000000000001"
	testDirID       = "00000000-0000-0000-0000-000000000002"
	testRunID       = "00000000-0000-0000-0000-000000000003"
	testEntryID     = "00000000-0000-0000-0000-000000000004"
)

func testContext() TaskContext {
	return TaskContext{
		Kind:          TaskKind,
		RunID:         testRunID,
		WorkspaceID:   testWorkspaceID,
		DirID:         testDirID,
		Scope:         "deployment",
		PromptVersion: PromptVersion,
		Entries: []EntrySnapshot{{
			ID:             testEntryID,
			Key:            "deployment-access",
			Content:        "Use exec isolation for deployment access.",
			ContentSHA256:  strings.Repeat("a", 64),
			CurrentVersion: 3,
			LifecycleState: "active",
		}},
	}
}

func testEvidence(source, ref, excerpt string) Evidence {
	return Evidence{
		SourceType:   source,
		ImmutableRef: ref,
		ObservedAt:   "2026-10-31T09:00:00Z",
		Excerpt:      excerpt,
	}
}

func testFinding(verdict string) Finding {
	entry := testContext().Entries[0]
	return Finding{
		EntryID:           entry.ID,
		Key:               entry.Key,
		OriginalContent:   entry.Content,
		ContentVersion:    entry.CurrentVersion,
		ContentSHA256:     entry.ContentSHA256,
		Scope:             "deployment",
		Verdict:           verdict,
		ProblemCategories: []string{"obsolete_operation"},
		CounterEvidence: []Evidence{
			testEvidence("code", "git:abc123", "The current deployment code uses SSH transport."),
		},
		ApplicableScope: "deployment automation",
		Recommendation:  "Review the current rule before any lifecycle transition.",
	}
}

func parseTestOutput(t *testing.T, output RunOutput) (RunOutput, error) {
	t.Helper()
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	return ParseRunOutput(string(raw), testContext())
}

func TestPromptFromContextKeepsAdvisoryEvidenceBoundary(t *testing.T) {
	raw, err := json.Marshal(testContext())
	if err != nil {
		t.Fatal(err)
	}
	prompt, ok := PromptFromContext(raw)
	if !ok {
		t.Fatal("PromptFromContext returned false")
	}
	for _, required := range []string{
		"Do not create, update, comment on, or change any Issue, PR, code, configuration, or knowledge entry.",
		"Review evidence newest to oldest: Owner corrections first, then Issues and PRs, then current code, then runtime records.",
		"writeback_authorized must always be false.",
		`key="deployment-access"`,
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("prompt missing %q", required)
		}
	}
}

func TestParseRunOutputRequiresCoverageForValidFinding(t *testing.T) {
	finding := testFinding("valid")
	_, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err == nil || !strings.Contains(err.Error(), "coverage") {
		t.Fatalf("expected retained coverage rejection, got %v", err)
	}

	parsed, err := parseTestOutput(t, RunOutput{
		Findings: []Finding{finding},
		RetainedCoverage: []RetainedCoverage{{
			EntryID:                testEntryID,
			CoverageEvidence:       testEvidence("code", "git:def456", "The current path still reads this rule."),
			SampledCounterevidence: testEvidence("issue", "RUYI-916#evidence", "No newer contradiction was found."),
		}},
		BackupReference: "no writeback authorized",
	})
	if err != nil {
		t.Fatalf("expected valid report, got %v", err)
	}
	if len(parsed.RetainedCoverage) != 1 {
		t.Fatalf("expected one retained coverage record, got %d", len(parsed.RetainedCoverage))
	}
}

func TestParseRunOutputKeepsNeedsReviewAdvisory(t *testing.T) {
	finding := testFinding("needs_review")
	parsed, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err != nil {
		t.Fatalf("expected needs_review report to remain advisory, got %v", err)
	}
	if parsed.Findings[0].WritebackAuthorized {
		t.Fatal("needs_review report authorized writeback")
	}
}

func TestParseRunOutputAcceptsInvalidFinding(t *testing.T) {
	finding := testFinding("invalid")
	parsed, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err != nil {
		t.Fatalf("expected invalid report to validate, got %v", err)
	}
	if parsed.Findings[0].Verdict != "invalid" {
		t.Fatalf("expected invalid verdict, got %q", parsed.Findings[0].Verdict)
	}
}

func TestParseRunOutputRejectsAgentWritebackAuthorization(t *testing.T) {
	finding := testFinding("deprecated")
	finding.WritebackAuthorized = true
	_, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err == nil || !strings.Contains(err.Error(), "authorize writeback") {
		t.Fatalf("expected writeback authorization rejection, got %v", err)
	}
}

func TestParseRunOutputAcceptsSSHEvidenceReplacingObsoleteExecRule(t *testing.T) {
	finding := testFinding("superseded")
	finding.CounterEvidence = []Evidence{
		testEvidence("owner_correction", "RUYI-913#correction", "Use SSH transport; the earlier exec isolation guidance is obsolete."),
		testEvidence("code", "git:current-deploy", "The deployment path creates an SSH session."),
	}
	finding.Recommendation = "Submit an Owner-reviewed lifecycle transition from exec isolation to SSH transport guidance."
	parsed, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "backup:knowledge-governance-run",
	})
	if err != nil {
		t.Fatalf("expected SSH replacement report to validate, got %v", err)
	}
	if parsed.Findings[0].Verdict != "superseded" {
		t.Fatalf("expected superseded verdict, got %q", parsed.Findings[0].Verdict)
	}
}

func TestParseRunOutputRejectsUnsupportedEvidence(t *testing.T) {
	finding := testFinding("needs_review")
	finding.CounterEvidence[0].SourceType = "model_output"
	_, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown source type") {
		t.Fatalf("expected unsupported evidence rejection, got %v", err)
	}
}

func TestParseRunOutputRequiresProblemCategory(t *testing.T) {
	finding := testFinding("needs_review")
	finding.ProblemCategories = nil
	_, err := parseTestOutput(t, RunOutput{
		Findings:        []Finding{finding},
		BackupReference: "no writeback authorized",
	})
	if err == nil || !strings.Contains(err.Error(), "supported conclusion") {
		t.Fatalf("expected missing problem category rejection, got %v", err)
	}
}
