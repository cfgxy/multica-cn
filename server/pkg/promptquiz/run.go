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

// Outcome values stored in prompt_quiz_result.outcome (migration 933).
//
// THESE ARE NOT GRADES. Nothing in RUYI-185 judges whether an answer is
// correct: the only thing observable from a terminal run is whether it produced
// an answer. The vocabulary says exactly that and no more, so that no reader
// can present it as a pass rate — a number that would have no computation
// behind it.
const (
	// OutcomeAnswered is a run that produced an answer to the question. Whether
	// the answer is any good is a separate judgement, and when it is made it
	// gets its own field rather than overloading this one.
	OutcomeAnswered = "answered"
	// OutcomeErrored is a run that produced no answer at all — timeout,
	// provider outage, cancellation. It is NOT a prompt failure and is
	// excluded from the sample, the same split prompt_quality_daily makes
	// between attributable and excluded failed runs.
	OutcomeErrored = "errored"
)

// OutcomeForStatus maps a terminal agent_task_queue status onto a measurement
// outcome.
//
// 'failed' and 'cancelled' both become errored: the queue status says the RUN
// did not finish, which is precisely "no answer to read".
func OutcomeForStatus(status string) string {
	if status == "completed" {
		return OutcomeAnswered
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
