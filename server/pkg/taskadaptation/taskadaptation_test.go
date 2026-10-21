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
	snapshot := &Snapshot{PolicyID: "policy-7", PolicyVersion: "1", Scope: "current_task", Trigger: "trigger", CurrentStrategy: "current", SuggestedStrategy: "suggested", AllowedActions: []string{"bounded action"}}
	for _, status := range []string{"applied", "retained", "blocked"} {
		output, result := ExtractReceipt("work completed\n"+receiptPrefix+status+receiptSuffix+"\n", snapshot)
		if output != "work completed" || result == nil || result.PolicyID != snapshot.PolicyID || result.Status != status {
			t.Errorf("ExtractReceipt(%q) = %q, %#v", status, output, result)
		}
	}
	for _, output := range []string{
		"work completed",
		"work completed\n" + receiptPrefix + "unknown" + receiptSuffix,
		receiptPrefix + "applied" + receiptSuffix + "\nmore output",
		receiptPrefix + "retained" + receiptSuffix + "\n" + receiptPrefix + "applied" + receiptSuffix,
	} {
		cleaned, result := ExtractReceipt(output, snapshot)
		if cleaned != output || result != nil {
			t.Errorf("invalid receipt changed output: %q, %#v", cleaned, result)
		}
	}
	if cleaned, result := ExtractReceipt(receiptPrefix+"applied"+receiptSuffix, nil); cleaned != receiptPrefix+"applied"+receiptSuffix || result != nil {
		t.Fatalf("disabled adaptation changed output: %q, %#v", cleaned, result)
	}
}
