package promptquiz

// Whether a question still tells the versions apart (RUYI-185 review, A4).
//
// A question earns its place in the bank by producing DIFFERENT readings for
// different prompts. One that returns the same number every time contributes no
// information to the comparison while costing a run on every sweep, and — worse
// — pads N, so a version can reach its required sample size on questions that
// could not have detected anything.
//
// This is a MARK, not an action: it is shown where the bank is maintained and
// nothing retires, reweights or regenerates an item automatically. Automatic
// difficulty iteration is explicitly out of scope; a question's value is a
// judgement its author can make once the mark tells them where to look.
type Discrimination string

const (
	// DiscriminationPending means not enough readings to judge. It is not "fine"
	// — a new question starts here and stays until it has been asked enough
	// times for its spread to mean anything.
	DiscriminationPending Discrimination = "pending"
	// DiscriminationNoSignal is the floor case: the question has been asked
	// enough times, and not one of those runs produced a readable answer. The
	// reading cannot be flat-because-easy or flat-because-hard; there is no
	// reading at all.
	DiscriminationNoSignal Discrimination = "no_signal"
	// DiscriminationFlat means the readings are effectively identical: the
	// question separates nothing.
	DiscriminationFlat Discrimination = "flat"
	// DiscriminationOK means the question's readings spread enough to carry
	// information.
	DiscriminationOK Discrimination = "ok"
)

// FlatIQRRatio is where "effectively zero spread" is drawn, as dispersion
// relative to level.
//
// WHY RELATIVE AND NOT ABSOLUTE. The measured dimension is run tokens, which has
// no natural ceiling — "pinned to the top" cannot be defined as a constant the
// way it can for a 0..1 score. What CAN be defined is "this question's answers
// are all the same size", and that is scale-free.
//
// WHY 0.02. The calibration sample in baseline.go records the relative
// dispersion actually observed on this deployment: agent f65fc145 runs
// IQR/median = 0.249. A threshold an order of magnitude below the quietest real
// distribution measured cannot mark a live question by accident, which is the
// error that matters — a false "flat" mark invites an author to delete a working
// question.
const FlatIQRRatio = 0.02

// DiscriminationMinSample is how many readings a verdict needs.
//
// Deliberately far below NewVersionSampleSize: this judgement is about one
// question's own spread, not about detecting a shift between two groups, and
// five identical readings are already evidence of flatness. Below it the IQR of
// a two- or three-point sample is mostly an artefact of which two runs landed.
const DiscriminationMinSample = 5

// DiscriminationFor judges one question from its readings within one cohort.
//
// attempts counts every stored measurement of the question, graded counts the
// ones that produced a readable value, and median/iqr describe those values.
// Separating the two counts is what distinguishes "never answers" from "not
// asked often enough yet" — collapsing them would report a question that fails
// on every run as merely unmeasured.
func DiscriminationFor(attempts, graded int, median, iqr float64) Discrimination {
	if graded == 0 {
		if attempts >= DiscriminationMinSample {
			return DiscriminationNoSignal
		}
		return DiscriminationPending
	}
	if graded < DiscriminationMinSample {
		return DiscriminationPending
	}
	if median <= 0 {
		// Every answer cost nothing measurable. Not a level to take a ratio
		// against, and not a question that can separate anything either.
		return DiscriminationFlat
	}
	if iqr/median <= FlatIQRRatio {
		return DiscriminationFlat
	}
	return DiscriminationOK
}
