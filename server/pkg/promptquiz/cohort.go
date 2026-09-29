package promptquiz

// A sample group is only a sample group if every reading in it was taken the
// same way. The statistics in baseline.go answer "do these two groups differ";
// they cannot answer "were these two groups measured with the same instrument",
// and a difference introduced by changing the instrument arrives looking exactly
// like a prompt regression.
//
// Three things decide how a reading was taken, and none of them is the prompt
// under test:
//
//	WHICH QUESTION, IN WHICH WORDING. Editing an item's body advances its
//	revision (and changes its digest). "Summarise this in one line" and
//	"Summarise this in one paragraph" are different questions; measurements of
//	the two are not repeats of one another. Migration 929 already said a reading
//	taken against different wording "is not comparable and is excluded by
//	revision" — this file is that exclusion.
//
//	WHICH RUNTIME AND MODEL. The same prompt on a different model costs a
//	different number of tokens for reasons that have nothing to do with the
//	prompt. Pinning the executed pair is the A1 ruling.
//
// The cohort is derived from the CURRENT version's newest reading, never
// supplied by the caller: the measuring stick is whatever the deployment is
// using now, and the baseline group has to be re-read through it. Readings that
// do not fit are excluded and COUNTED — silently dropping them would turn "the
// bank was edited, so the old curve no longer applies" into an unexplained
// change in N.

// Wording identifies one version of one question's public text.
//
// Both halves are kept, not just the revision: two rows with the same revision
// but different digests mean the revision counter was not bumped on an edit, and
// the digest is the fact while the counter is the bookkeeping. Comparing both
// makes the counter's failure show up as "not comparable" rather than as two
// different questions silently pooled.
type Wording struct {
	Revision int32
	BodySHA  string
}

// Fingerprint is everything about a reading except the reading itself.
type Fingerprint struct {
	ItemID string
	Wording
	// RuntimeID and Model are the pair that actually executed the run, empty
	// when the row was stored without them. Empty is a distinct key rather than
	// a wildcard: an unattributed reading matches nothing, including another
	// unattributed one from a different unknown runtime.
	RuntimeID string
	Model     string
}

// Measurement is one stored row, as the reader sees it.
type Measurement struct {
	Fingerprint
	Outcome string
	// Value is the reading, valid only when Valued is set. A run whose usage
	// rows never arrived reported no cost; storing that as 0 would let a
	// reporting outage read as a cost improvement.
	Value  float64
	Valued bool
	// Score is the graded reading (RUYI-286), valid only when Scored is set.
	// NULL on the row — check-less item, errored run, no answer text — is
	// Scored=false, the "measured but not graded" state; it never reads as 0
	// and never enters SummarizeScores.
	Score  float64
	Scored bool
}

// usable reports whether a row is a reading at all, independent of any cohort.
func (m Measurement) usable() bool {
	return m.Outcome != OutcomeErrored && m.Valued
}

// Cohort is the measuring stick: one runtime/model pair, and one wording per
// question.
type Cohort struct {
	RuntimeID string
	Model     string
	// wording is per item, because a bank edit retires one question's old
	// wording without invalidating the others. Requiring the whole bank to be
	// unedited would throw away every reading whenever any item changed.
	wording map[string]Wording
}

// CohortOf derives the measuring stick from a version's own readings.
//
// ms must be ordered newest first, which is the order ListPromptQuizSamples
// returns. The newest usable reading fixes the runtime/model pair; within that
// pair, each question's newest usable reading fixes its wording. Anything older
// that disagrees is a reading from a previous instrument.
//
// Reports false when the version has no usable reading yet: there is then
// nothing to define a cohort with, and nothing to compare either.
func CohortOf(ms []Measurement) (Cohort, bool) {
	c := Cohort{wording: map[string]Wording{}}
	found := false
	for _, m := range ms {
		if !m.usable() {
			continue
		}
		if !found {
			c.RuntimeID, c.Model = m.RuntimeID, m.Model
			found = true
		}
		if m.RuntimeID != c.RuntimeID || m.Model != c.Model {
			continue
		}
		if _, seen := c.wording[m.ItemID]; !seen {
			c.wording[m.ItemID] = m.Wording
		}
	}
	return c, found
}

// Includes reports whether a reading was taken with this cohort's instrument.
//
// Every clause here is an isolation condition, and cohort_test.go asserts for
// each one that removing it merges readings that must not be merged.
func (c Cohort) Includes(m Measurement) bool {
	if m.RuntimeID != c.RuntimeID || m.Model != c.Model {
		return false
	}
	w, ok := c.wording[m.ItemID]
	if !ok {
		return false
	}
	return m.Revision == w.Revision && m.BodySHA == w.BodySHA
}

// Select folds the readings this cohort can account for into a sample group,
// and reports how many usable readings it had to leave out.
//
// The excluded count is part of the reading, not diagnostics: it is the only
// way a reader can tell "this version has 4 measurements" from "this version
// has 4 measurements on the current bank and 26 taken before it was edited".
func (c Cohort) Select(ms []Measurement) (Sample, int) {
	var sample Sample
	excluded := 0
	for _, m := range ms {
		if !m.usable() {
			continue
		}
		if !c.Includes(m) {
			excluded++
			continue
		}
		sample.Values = append(sample.Values, m.Value)
		if m.Scored {
			sample.Scores = append(sample.Scores, ScoredReading{ItemID: m.ItemID, Score: m.Score})
		}
	}
	return sample, excluded
}
