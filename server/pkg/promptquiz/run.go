package promptquiz

import (
	"crypto/sha256"
	"encoding/hex"
)

// OriginatorSource is the tag every quiz run carries in
// agent_task_queue.originator_source. It is the isolation marker: production
// statistics filter it out, and the result collector refuses any run that does
// not carry it, so a production run can never be mistaken for a measurement
// even though both can have a NULL issue_id.
const OriginatorSource = "quiz"

// TaskKind is what computeTaskKind reports for a quiz run. Distinct from
// "quick_create" — both lack an issue, and without this tag the activity UI
// would file every measurement as a member creating an issue.
const TaskKind = "quiz"

// TriggerEvidenceKind names what caused a quiz run, paired with the item id in
// trigger_evidence_ref_id (the MUL-4302 evidence convention).
const TriggerEvidenceKind = "quiz_item"

// Outcome values stored in prompt_quiz_result.outcome.
const (
	// OutcomePassed / OutcomeFailed are the graded results.
	OutcomePassed = "passed"
	OutcomeFailed = "failed"
	// OutcomeErrored is a run that produced no answer at all — timeout,
	// provider outage, cancellation. It is NOT a prompt failure and is
	// excluded from the graded sample, the same split prompt_quality_daily
	// makes between attributable and excluded failed runs.
	OutcomeErrored = "errored"
)

// OutcomeForStatus maps a terminal agent_task_queue status onto a measurement
// outcome.
//
// 'failed' and 'cancelled' both become errored rather than failed: the queue
// status says the RUN did not finish, which says nothing about whether the
// prompt answered the question well. Grading a genuine answer is a separate
// judgement made on the run's output; this function only decides whether there
// is an answer to grade.
func OutcomeForStatus(status string) string {
	if status == "completed" {
		return OutcomePassed
	}
	return OutcomeErrored
}

// BodyDigest is the item-body fingerprint stored with every measurement, so a
// row stays interpretable after the bank is edited without keeping old bodies
// around. Hex sha256; the body itself never leaves the platform (Owner Q8) and
// a digest is not reversible into one.
func BodyDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
