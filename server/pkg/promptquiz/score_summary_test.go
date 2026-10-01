package promptquiz

import "testing"

// TestSummarizeScores pins the aggregation contract: only graded readings
// count, the mean is over readings (not item means), and the item breakdown
// comes back in stable id order. The "ungraded rows never read as 0" rule is
// upstream of this function now — Select only copies Scored rows into
// Sample.Scores, which TestSelectFillsScores pins.
func TestSummarizeScores(t *testing.T) {
	got := SummarizeScores(Sample{Scores: []ScoredReading{
		{ItemID: "item-b", Score: 1.0},
		{ItemID: "item-a", Score: 0.5},
		{ItemID: "item-a", Score: 0.0},
	}})
	if got.Graded != 3 {
		t.Fatalf("Graded = %d, want 3", got.Graded)
	}
	if got.Mean != 0.5 {
		t.Fatalf("Mean = %v, want 0.5 (readings 1.0+0.5+0.0 over 3)", got.Mean)
	}
	if len(got.Items) != 2 {
		t.Fatalf("Items = %d entries, want 2", len(got.Items))
	}
	// Stable id order: item-a before item-b regardless of map order.
	if got.Items[0].ItemID != "item-a" || got.Items[1].ItemID != "item-b" {
		t.Fatalf("item order = %s,%s; want item-a,item-b", got.Items[0].ItemID, got.Items[1].ItemID)
	}
	if got.Items[0].Graded != 2 || got.Items[0].Mean != 0.25 {
		t.Fatalf("item-a = %d@%v, want 2@0.25", got.Items[0].Graded, got.Items[0].Mean)
	}
}

// TestSummarizeScoresEmpty pins the ungraded group: Graded=0, no items —
// "not graded", which every reader must render as such, never as a zero
// score.
func TestSummarizeScoresEmpty(t *testing.T) {
	got := SummarizeScores(Sample{})
	if got.Graded != 0 || got.Mean != 0 || len(got.Items) != 0 {
		t.Fatalf("empty sample = %+v, want zero-valued with no items", got)
	}
	if got.Items == nil {
		t.Fatal("Items must be an empty slice, not nil, so the wire form is []")
	}
}

// TestSelectFillsScores pins the seam between the cohort filter and score
// aggregation: a sample's Scores carry exactly the graded rows the cohort
// can account for. Errored rows, ungraded rows, and rows taken with a
// different instrument must all stay out — a score that failed the same
// isolation its token sibling failed must not re-enter through the graded
// side door.
func TestSelectFillsScores(t *testing.T) {
	cohort, ok := CohortOf([]Measurement{
		{Fingerprint: Fingerprint{ItemID: "i", RuntimeID: "rt", Model: "m"}, Outcome: "answered", Value: 10, Valued: true},
	})
	if !ok {
		t.Fatal("cohort expected from one usable reading")
	}
	rows := []Measurement{
		// In-cohort graded: the only row that may reach Sample.Scores.
		{Fingerprint: Fingerprint{ItemID: "i", RuntimeID: "rt", Model: "m"}, Outcome: "answered", Value: 11, Valued: true, Score: 1.0, Scored: true},
		// In-cohort ungraded (check-less item): usable for tokens, absent from scores.
		{Fingerprint: Fingerprint{ItemID: "i", RuntimeID: "rt", Model: "m"}, Outcome: "answered", Value: 12, Valued: true},
		// Graded but errored: measured nothing.
		{Fingerprint: Fingerprint{ItemID: "i", RuntimeID: "rt", Model: "m"}, Outcome: OutcomeErrored, Score: 0.9, Scored: true},
		// Graded but different runtime: incomparable.
		{Fingerprint: Fingerprint{ItemID: "i", RuntimeID: "other", Model: "m"}, Outcome: "answered", Value: 13, Valued: true, Score: 0.8, Scored: true},
	}
	sample, excluded := cohort.Select(rows)
	if excluded != 1 {
		t.Fatalf("excluded = %d, want 1 (the other-runtime row)", excluded)
	}
	if len(sample.Scores) != 1 || sample.Scores[0].Score != 1.0 {
		t.Fatalf("Scores = %+v, want exactly the one in-cohort graded row", sample.Scores)
	}
	// The errored row was never usable, so it reaches neither side — that is
	// Select's pre-existing contract, and this pins that the graded side
	// doesn't change it.
	if len(sample.Values) != 2 {
		t.Fatalf("Values = %d entries, want 2 (usable in-cohort rows only)", len(sample.Values))
	}
}
