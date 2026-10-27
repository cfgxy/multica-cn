package promptquiz

// Score aggregation (RUYI-286): the graded side of a sample group, reported
// beside the existing cost distribution rather than folded into it.
//
// The aggregation is deliberately plain — counts and means over explicitly
// graded rows — because the score's unit (a weighted pass ratio over published
// assertions) is already the explanation. Where the token comparison needs a
// two-sample test because its unit (tokens) has no natural "correct", the
// score needs nothing more than "how many graded readings, what average" —
// anything fancier would be decoration on a number a reader can already
// recompute from the samples endpoint.
//
// The cohort discipline still applies: callers aggregate over cohort-selected
// samples (Cohort.Select), so a bank edit or a runtime switch splits a group
// here exactly where it splits the token comparison.

// ItemScore is one item's graded aggregate inside a sample group.
type ItemScore struct {
	ItemID string `json:"item_id"`
	// Graded is how many cohort readings of this item carry a score. A zero
	// means the item was asked but never graded (no checks, or every run
	// errored) — it must render as "not graded", never as 0%.
	Graded int `json:"graded"`
	// Mean is the mean weighted pass ratio over the graded readings, 0..1.
	Mean float64 `json:"mean"`
}

// ScoreSummary is one sample group's graded aggregate.
type ScoreSummary struct {
	// Graded counts graded readings in the group.
	Graded int `json:"graded"`
	// Mean is the mean score across ALL graded readings in the group (not a
	// mean of item means — an item measured twice weighs twice, which keeps
	// the number recomputable from the samples endpoint).
	Mean float64 `json:"mean"`
	// Items carries the per-item breakdown, ordered by item id for stable
	// rendering. Empty when nothing in the group graded.
	Items []ItemScore `json:"items"`
}

// SummarizeScores folds one cohort-selected sample into its score summary.
//
// Errored rows have no score by construction (the collector leaves it NULL),
// and rows of a check-less item are NULL too; both simply do not count. There
// is no "0" anywhere in this path: an ungraded group is Graded=0 with no
// items, and every reader must show that as "not graded".
func SummarizeScores(sample Sample) ScoreSummary {
	type acc struct {
		n   int
		sum float64
	}
	perItem := map[string]*acc{}
	total, sum := 0, 0.0
	for _, m := range sample.Scores {
		total++
		sum += m.Score
		a := perItem[m.ItemID]
		if a == nil {
			a = &acc{}
			perItem[m.ItemID] = a
		}
		a.n++
		a.sum += m.Score
	}
	out := ScoreSummary{Graded: total}
	if total > 0 {
		out.Mean = sum / float64(total)
	}
	for itemID, a := range perItem {
		out.Items = append(out.Items, ItemScore{ItemID: itemID, Graded: a.n, Mean: a.sum / float64(a.n)})
	}
	// map iteration is random; order by item id so the wire form (and
	// therefore any snapshot a reader diffs) is stable across calls.
	for i := 1; i < len(out.Items); i++ {
		for j := i; j > 0 && out.Items[j].ItemID < out.Items[j-1].ItemID; j-- {
			out.Items[j], out.Items[j-1] = out.Items[j-1], out.Items[j]
		}
	}
	if out.Items == nil {
		out.Items = []ItemScore{}
	}
	return out
}
