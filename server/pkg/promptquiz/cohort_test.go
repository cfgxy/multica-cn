package promptquiz

import "testing"

// The isolation conditions in Cohort.Includes are what keep a bank edit or a
// runtime swap from changing the measuring stick under a live curve. Each test
// below asserts the separation AND then re-runs the same data through a copy of
// Select with exactly one clause removed, asserting that the copy merges what
// must not be merged. The second half is the reverse verification: it fails if
// the clause it drops has stopped being load-bearing, which is the only way a
// passing isolation test can be mistaken for a tautology.

// dropped names the isolation clause a relaxed selector leaves out.
type dropped int

const (
	dropWording dropped = iota // ignore item_revision / item_body_sha256
	dropRuntime                // ignore runtime_id / run_model
)

// selectRelaxed is Cohort.Select with one clause removed. It is deliberately a
// copy rather than a flag on the real function: production code must not carry a
// switch that turns the isolation off.
func selectRelaxed(c Cohort, ms []Measurement, drop dropped) Sample {
	var sample Sample
	for _, m := range ms {
		if !m.usable() {
			continue
		}
		if drop != dropRuntime && (m.RuntimeID != c.RuntimeID || m.Model != c.Model) {
			continue
		}
		w, known := c.wording[m.ItemID]
		if !known {
			continue
		}
		if drop != dropWording && (m.Revision != w.Revision || m.BodySHA != w.BodySHA) {
			continue
		}
		sample.Values = append(sample.Values, m.Value)
	}
	return sample
}

func reading(item string, rev int32, sha, runtime, model string, v float64) Measurement {
	return Measurement{
		Fingerprint: Fingerprint{
			ItemID:    item,
			Wording:   Wording{Revision: rev, BodySHA: sha},
			RuntimeID: runtime,
			Model:     model,
		},
		Outcome: OutcomeAnswered,
		Value:   v,
		Valued:  true,
	}
}

// Two revisions of one question inside ONE version. This is the defect the
// cohort exists for: editing the bank while a version is accumulating its sample
// used to pool readings of the old and the new wording, so the curve changed
// because the question changed.
func TestCohortDoesNotMergeTwoRevisionsOfTheSameItem(t *testing.T) {
	// Newest first, as ListPromptQuizSamples returns.
	rows := []Measurement{
		reading("item-a", 2, "sha-2", "rt", "m", 100),
		reading("item-a", 2, "sha-2", "rt", "m", 110),
		reading("item-a", 1, "sha-1", "rt", "m", 900),
		reading("item-a", 1, "sha-1", "rt", "m", 910),
	}
	c, ok := CohortOf(rows)
	if !ok {
		t.Fatal("CohortOf found no cohort in four usable readings")
	}
	if got := c.wording["item-a"]; got.Revision != 2 {
		t.Fatalf("cohort wording revision %d, want 2 — the newest reading defines the stick", got.Revision)
	}

	sample, excluded := c.Select(rows)
	if len(sample.Values) != 2 {
		t.Errorf("sample has %d values (%v), want the 2 taken against revision 2 — revision 1 readings were pooled in",
			len(sample.Values), sample.Values)
	}
	if excluded != 2 {
		t.Errorf("excluded count %d, want 2 — readings dropped from the group must stay visible to the reader", excluded)
	}

	// Reverse verification: without the wording clause the same data pools all
	// four. If this ever stops holding, the assertion above proves nothing.
	if merged := selectRelaxed(c, rows, dropWording); len(merged.Values) != 4 {
		t.Errorf("relaxed selector kept %d values, want 4: the wording clause is no longer what separates the revisions, so the test above is vacuous",
			len(merged.Values))
	}
}

// Same revision counter, different body digest: the counter was not bumped on an
// edit. The digest is the fact and the counter is the bookkeeping, so this reads
// as "not comparable" rather than as one wording.
func TestCohortDoesNotMergeDifferentDigestsAtTheSameRevision(t *testing.T) {
	rows := []Measurement{
		reading("item-a", 1, "sha-new", "rt", "m", 100),
		reading("item-a", 1, "sha-old", "rt", "m", 900),
	}
	c, ok := CohortOf(rows)
	if !ok {
		t.Fatal("CohortOf found no cohort")
	}
	sample, excluded := c.Select(rows)
	if len(sample.Values) != 1 || sample.Values[0] != 100 {
		t.Errorf("sample = %v, want [100] only — a reading whose digest disagrees is not a repeat", sample.Values)
	}
	if excluded != 1 {
		t.Errorf("excluded count %d, want 1", excluded)
	}
	if merged := selectRelaxed(c, rows, dropWording); len(merged.Values) != 2 {
		t.Errorf("relaxed selector kept %d values, want 2: the digest comparison is no longer load-bearing", len(merged.Values))
	}
}

// A bank edit to ONE question must not throw away the other questions' readings.
// The wording map is per item for this reason.
func TestCohortKeepsOtherItemsWhenOneIsEdited(t *testing.T) {
	rows := []Measurement{
		reading("item-a", 2, "a2", "rt", "m", 100),
		reading("item-b", 1, "b1", "rt", "m", 200),
		reading("item-a", 1, "a1", "rt", "m", 900),
		reading("item-b", 1, "b1", "rt", "m", 210),
	}
	c, _ := CohortOf(rows)
	sample, excluded := c.Select(rows)
	if len(sample.Values) != 3 {
		t.Errorf("sample has %d values (%v), want 3 — editing item-a must not retire item-b's readings",
			len(sample.Values), sample.Values)
	}
	if excluded != 1 {
		t.Errorf("excluded count %d, want 1", excluded)
	}
}

// The A1 ruling: the same prompt on a different runtime/model costs a different
// number of tokens for reasons that have nothing to do with the prompt, so the
// two sets of readings may not share a curve.
func TestCohortDoesNotMergeTwoRuntimes(t *testing.T) {
	rows := []Measurement{
		reading("item-a", 1, "a1", "rt-new", "sonnet", 100),
		reading("item-a", 1, "a1", "rt-new", "sonnet", 110),
		reading("item-a", 1, "a1", "rt-old", "sonnet", 900),
		reading("item-a", 1, "a1", "rt-new", "opus", 950),
	}
	c, ok := CohortOf(rows)
	if !ok {
		t.Fatal("CohortOf found no cohort")
	}
	if c.RuntimeID != "rt-new" || c.Model != "sonnet" {
		t.Fatalf("cohort instrument = (%q, %q), want (rt-new, sonnet)", c.RuntimeID, c.Model)
	}
	sample, excluded := c.Select(rows)
	if len(sample.Values) != 2 {
		t.Errorf("sample has %d values (%v), want the 2 from (rt-new, sonnet)", len(sample.Values), sample.Values)
	}
	if excluded != 2 {
		t.Errorf("excluded count %d, want 2 — one other runtime and one other model", excluded)
	}

	if merged := selectRelaxed(c, rows, dropRuntime); len(merged.Values) != 4 {
		t.Errorf("relaxed selector kept %d values, want 4: the runtime/model clause is no longer what separates the instruments",
			len(merged.Values))
	}
}

// An unattributed reading (stored before the runtime was recorded, or by a queue
// row migration 251 left without one) matches nothing — including another
// unattributed reading, which could have come from any runtime at all.
func TestCohortTreatsUnknownRuntimeAsItsOwnKey(t *testing.T) {
	attributed := []Measurement{
		reading("item-a", 1, "a1", "rt", "m", 100),
		reading("item-a", 1, "a1", "", "", 900),
	}
	c, _ := CohortOf(attributed)
	sample, excluded := c.Select(attributed)
	if len(sample.Values) != 1 || excluded != 1 {
		t.Errorf("sample = %v, excluded = %d; want one value and one exclusion — an unknown instrument is not the known one",
			sample.Values, excluded)
	}

	// And the reverse direction: when the newest reading is itself unattributed,
	// the cohort it defines does not swallow the attributed ones.
	unattributed := []Measurement{
		reading("item-a", 1, "a1", "", "", 100),
		reading("item-a", 1, "a1", "rt", "m", 900),
	}
	uc, _ := CohortOf(unattributed)
	usample, uexcluded := uc.Select(unattributed)
	if len(usample.Values) != 1 || uexcluded != 1 {
		t.Errorf("sample = %v, excluded = %d; want one value and one exclusion", usample.Values, uexcluded)
	}
}

// Errored rows and rows whose usage never arrived are not readings, so they
// cannot define the instrument either — otherwise an outage on a new model would
// re-point the whole curve at it.
func TestCohortIgnoresUnusableRowsWhenDerivingTheInstrument(t *testing.T) {
	rows := []Measurement{
		{Fingerprint: Fingerprint{ItemID: "item-a", Wording: Wording{1, "a1"}, RuntimeID: "rt-broken", Model: "m"},
			Outcome: OutcomeErrored},
		// Answered but no usage rows: a measurement of nothing.
		{Fingerprint: Fingerprint{ItemID: "item-a", Wording: Wording{1, "a1"}, RuntimeID: "rt-silent", Model: "m"},
			Outcome: OutcomeAnswered},
		reading("item-a", 1, "a1", "rt-good", "m", 100),
	}
	c, ok := CohortOf(rows)
	if !ok {
		t.Fatal("CohortOf found no cohort although one reading is usable")
	}
	if c.RuntimeID != "rt-good" {
		t.Errorf("cohort runtime = %q, want rt-good — an errored or unmeasured row must not define the instrument", c.RuntimeID)
	}
	sample, excluded := c.Select(rows)
	if len(sample.Values) != 1 {
		t.Errorf("sample = %v, want one value", sample.Values)
	}
	if excluded != 0 {
		t.Errorf("excluded = %d, want 0 — unusable rows are not exclusions, they were never readings", excluded)
	}
}

func TestCohortOfReportsNoCohortWithoutAUsableReading(t *testing.T) {
	rows := []Measurement{
		{Fingerprint: Fingerprint{ItemID: "item-a"}, Outcome: OutcomeErrored},
		{Fingerprint: Fingerprint{ItemID: "item-a"}, Outcome: OutcomeAnswered},
	}
	if _, ok := CohortOf(rows); ok {
		t.Error("CohortOf reported a cohort from rows that contain no reading")
	}
	if _, ok := CohortOf(nil); ok {
		t.Error("CohortOf reported a cohort from no rows")
	}
}

// The baseline group is read through the CURRENT version's stick, which is the
// whole point: a version that was measured before a bank edit contributes only
// the readings still comparable with what the deployment measures now.
func TestBaselineGroupIsReadThroughTheCurrentCohort(t *testing.T) {
	current := []Measurement{
		reading("item-a", 2, "a2", "rt", "m", 100),
		reading("item-a", 2, "a2", "rt", "m", 105),
	}
	baseline := []Measurement{
		reading("item-a", 2, "a2", "rt", "m", 200),
		reading("item-a", 1, "a1", "rt", "m", 900),
		reading("item-a", 1, "a1", "rt", "m", 910),
	}
	c, _ := CohortOf(current)
	sample, excluded := c.Select(baseline)
	if len(sample.Values) != 1 || sample.Values[0] != 200 {
		t.Errorf("baseline sample = %v, want [200] — only the reading taken against the current wording", sample.Values)
	}
	if excluded != 2 {
		t.Errorf("baseline excluded = %d, want 2; a group that shrank from 3 to 1 must say why", excluded)
	}
}
