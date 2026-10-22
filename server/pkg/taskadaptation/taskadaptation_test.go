package taskadaptation

import (
	"encoding/json"
	"testing"
)

func TestSnapshotFromContextIsOptInAndFailClosed(t *testing.T) {
	valid := json.RawMessage(`{"capability_adaptation":{"policy_id":"p-1","policy_version":"2","scope":"current_task","trigger":"task needs a concise plan","current_strategy":"plan fully","suggested_strategy":"plan in short steps","allowed_actions":["use short checkpoints"]}}`)
	if got := SnapshotFromContext(valid); got == nil || got.PolicyID != "p-1" {
		t.Fatalf("valid snapshot = %#v", got)
	}
	for _, raw := range []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"capability_adaptation":{"policy_id":"p-1","policy_version":"2","scope":"cross_task","trigger":"x","current_strategy":"x","suggested_strategy":"y","allowed_actions":["z"]}}`),
		json.RawMessage(`{"capability_adaptation":{"policy_id":"p-1","policy_version":"2","scope":"current_task","trigger":"x","current_strategy":"x","suggested_strategy":"y","allowed_actions":[]}}`),
		json.RawMessage(`{"capability_adaptation":{"policy_id":"p-1","policy_version":"2","scope":"current_task","trigger":"x\ny","current_strategy":"x","suggested_strategy":"y","allowed_actions":["z"]}}`),
		json.RawMessage(`not-json`),
	} {
		if got := SnapshotFromContext(raw); got != nil {
			t.Errorf("invalid or disabled context %s produced %#v", raw, got)
		}
	}
}

func TestExtractReceiptRequiresEnabledSnapshotAndExactFinalStatus(t *testing.T) {
	snapshot := &Snapshot{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "1", Scope: "current_task", Trigger: "trigger", CurrentStrategy: "current", SuggestedStrategy: "suggested", AllowedActions: []string{"bounded action"}}
	for _, status := range []string{"applied", "retained", "blocked"} {
		output, result := ExtractReceipt("work completed\n"+receiptPrefix+"task-a: "+status+receiptSuffix+"\n", snapshot, "task-a")
		if output != "work completed" || result == nil || result.TaskID != "task-a" || result.PolicyID != snapshot.PolicyID || result.PolicyVersion != snapshot.PolicyVersion || result.Status != status {
			t.Errorf("ExtractReceipt(%q) = %q, %#v", status, output, result)
		}
	}
	for _, tc := range []struct {
		name   string
		input  string
		output string
	}{
		{name: "no footer", input: "work completed", output: "work completed"},
		{name: "unknown status", input: "work completed\n" + receiptPrefix + "task-a: unknown" + receiptSuffix, output: "work completed"},
		{name: "wrong task", input: "work completed\n" + receiptPrefix + "task-b: applied" + receiptSuffix, output: "work completed"},
		{name: "non-final", input: receiptPrefix + "task-a: applied" + receiptSuffix + "\nmore output", output: "more output"},
		{name: "duplicate", input: receiptPrefix + "task-a: retained" + receiptSuffix + "\n" + receiptPrefix + "task-a: applied" + receiptSuffix, output: ""},
	} {
		cleaned, result := ExtractReceipt(tc.input, snapshot, "task-a")
		if cleaned != tc.output || result != nil {
			t.Errorf("%s: ExtractReceipt() = %q, %#v; want %q, nil", tc.name, cleaned, result, tc.output)
		}
	}
	if cleaned, result := ExtractReceipt("answer\n"+receiptPrefix+"task-a: applied"+receiptSuffix, nil, "task-a"); cleaned != "answer" || result != nil {
		t.Fatalf("disabled adaptation leaked internal footer: %q, %#v", cleaned, result)
	}
}

func TestOutcomeMustMatchExactPolicySnapshot(t *testing.T) {
	snapshot := &Snapshot{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "2", Scope: "current_task", Trigger: "trigger", CurrentStrategy: "current", SuggestedStrategy: "suggested", AllowedActions: []string{"bounded action"}}
	for _, outcome := range []Outcome{
		{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "2", Status: "applied"},
		{TaskID: "task-b", PolicyID: "policy-7", PolicyVersion: "2", Status: "applied"},
		{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "1", Status: "applied"},
		{TaskID: "task-a", PolicyID: "policy-8", PolicyVersion: "2", Status: "applied"},
		{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "2", Status: "unknown"},
		{TaskID: "task-a", PolicyID: "policy-7", Status: "applied"},
	} {
		got := outcome.Matches(snapshot, "task-a")
		want := outcome.TaskID == "task-a" && outcome.PolicyID == snapshot.PolicyID && outcome.PolicyVersion == snapshot.PolicyVersion && ValidStatus(outcome.Status)
		if got != want {
			t.Errorf("Outcome(%+v).Matches() = %v, want %v", outcome, got, want)
		}
	}
	if (Outcome{TaskID: "task-a", PolicyID: "policy-7", PolicyVersion: "2", Status: "applied"}).Matches(nil, "task-a") {
		t.Fatal("outcome matched a missing snapshot")
	}
}

func TestValidateExperienceCandidateRequiresRepeatedMatchingAppliedReceipts(t *testing.T) {
	snapshot := func(taskID string) *Snapshot {
		return &Snapshot{
			TaskID: taskID, PolicyID: "policy-7", PolicyVersion: "2", Scope: "current_task",
			Trigger: "large implementation", CurrentStrategy: "one pass",
			SuggestedStrategy: "use verified steps", AllowedActions: []string{"split into checkpoints"},
		}
	}
	first, second := snapshot("task-a"), snapshot("task-b")
	candidateID := CandidateID(first)
	valid := []ExperienceReceipt{
		{TaskID: first.TaskID, Snapshot: first, Outcome: &Outcome{TaskID: first.TaskID, PolicyID: first.PolicyID, PolicyVersion: first.PolicyVersion, Status: "applied"}},
		{TaskID: second.TaskID, Snapshot: second, Outcome: &Outcome{TaskID: second.TaskID, PolicyID: second.PolicyID, PolicyVersion: second.PolicyVersion, Status: "applied"}},
	}
	if err := ValidateExperienceCandidate(candidateID, valid); err != nil {
		t.Fatalf("valid repeated evidence rejected: %v", err)
	}
	for _, tc := range []struct {
		name      string
		candidate string
		receipts  []ExperienceReceipt
	}{
		{name: "single source", candidate: candidateID, receipts: valid[:1]},
		{name: "wrong fingerprint", candidate: "other", receipts: valid},
		{name: "duplicate source", candidate: candidateID, receipts: []ExperienceReceipt{valid[0], valid[0]}},
		{name: "different strategy", candidate: candidateID, receipts: []ExperienceReceipt{
			valid[0], {TaskID: second.TaskID, Snapshot: func() *Snapshot {
				changed := *second
				changed.SuggestedStrategy = "unverified strategy"
				return &changed
			}(),
				Outcome: &Outcome{TaskID: second.TaskID, PolicyID: second.PolicyID, PolicyVersion: second.PolicyVersion, Status: "applied"}},
		}},
		{name: "not applied", candidate: candidateID, receipts: []ExperienceReceipt{
			valid[0], {TaskID: second.TaskID, Snapshot: second, Outcome: &Outcome{TaskID: second.TaskID, PolicyID: second.PolicyID, PolicyVersion: second.PolicyVersion, Status: "retained"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExperienceCandidate(tc.candidate, tc.receipts); err == nil {
				t.Fatal("invalid experience evidence was accepted")
			}
		})
	}
}
