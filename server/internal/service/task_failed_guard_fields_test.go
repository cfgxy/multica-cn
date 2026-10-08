package service

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// The failure event is where a delivery-guard refusal becomes legible to the
// user (RUYI-579 W3): the classified kind and the self-heal attempt ride as
// structured fields, while the machine-readable trailer itself never leaks
// into the human-facing error text — the server stores and broadcasts the
// remainder only.
func TestTaskFailedFieldsCarryGuardClassification(t *testing.T) {
	msg := taskfailure.AppendGuardMeta(
		"refusing to record branch agent/j/mul-6881: the delivered commit no longer contains the commit this turn started from",
		taskfailure.GuardKindAncestorBreak, true)

	fields := taskFailedFields(msg, string(taskfailure.ReasonDeliveryGuard), false)

	if fields["guard_kind"] != taskfailure.GuardKindAncestorBreak {
		t.Errorf("guard_kind = %v, want %q", fields["guard_kind"], taskfailure.GuardKindAncestorBreak)
	}
	if fields["guard_heal_attempted"] != true {
		t.Errorf("guard_heal_attempted = %v, want true", fields["guard_heal_attempted"])
	}
	errText, _ := fields["error"].(string)
	if errText == "" {
		t.Fatal("unclassified retry-pending=false failure carries no error text")
	}
	if strings.Contains(errText, taskfailure.GuardMetaLine) {
		t.Errorf("the guard trailer leaked into the event error: %q", errText)
	}
	if !strings.Contains(errText, "refusing to record branch agent/j/mul-6881") {
		t.Errorf("event error lost the refusal prose: %q", errText)
	}
}

// An ordinary failure has nothing to classify: no guard fields may appear,
// or consumers would read an empty kind as a guard refusal.
func TestTaskFailedFieldsWithoutGuardTrailerStayUnchanged(t *testing.T) {
	fields := taskFailedFields("runtime went offline", "runtime_offline", false)

	if _, ok := fields["guard_kind"]; ok {
		t.Errorf("plain failure grew a guard_kind: %v", fields["guard_kind"])
	}
	if _, ok := fields["guard_heal_attempted"]; ok {
		t.Errorf("plain failure grew guard_heal_attempted: %v", fields["guard_heal_attempted"])
	}
	if text, _ := fields["error"].(string); text != "runtime went offline" {
		t.Errorf("error = %q, want the message unchanged", text)
	}
}

// A retry-pending failure never carries error text, but the classification
// still rides: the user is told what kind of refusal will be retried.
func TestTaskFailedFieldsRetryPendingKeepsKindDropsError(t *testing.T) {
	msg := taskfailure.AppendGuardMeta("refusing to record branch x", taskfailure.GuardKindBranchMismatch, false)

	fields := taskFailedFields(msg, string(taskfailure.ReasonDeliveryGuard), true)

	if _, ok := fields["error"]; ok {
		t.Errorf("retry-pending failure carries error text: %v", fields["error"])
	}
	if fields["guard_kind"] != taskfailure.GuardKindBranchMismatch {
		t.Errorf("guard_kind = %v, want %q", fields["guard_kind"], taskfailure.GuardKindBranchMismatch)
	}
	if fields["guard_heal_attempted"] != false {
		t.Errorf("guard_heal_attempted = %v, want false", fields["guard_heal_attempted"])
	}
}
