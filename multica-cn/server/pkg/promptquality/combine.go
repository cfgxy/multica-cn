package promptquality

import "sort"

// Combining daily buckets into the window the dashboard asks for.
//
// Every counted dimension is additive, so a window is the sum of its days and
// nothing is lost. The two medians are not: a median of medians is not the
// median of the underlying runs, and recovering the real one would mean
// storing every run's token count forever. The approximation is taken
// deliberately and weighted by the runs behind each day, so a day with two
// runs cannot pull the window's median as hard as a day with two hundred.
//
// This is the only lossy step in the pipeline, and it is confined to D1's
// per-run median and D2's score. Callers that need an exact median ask for a
// single day, which is stored exactly.

// Combine folds several daily buckets into one window.
func Combine(days []Result) Result {
	out := Result{FailureReasonCounts: map[string]int{}}
	if len(days) == 0 {
		return out
	}

	var (
		runTokens  []weighted
		discipline []weighted
	)

	for _, d := range days {
		out.FinishedRuns += d.FinishedRuns
		out.DisciplineCoveredRuns += d.DisciplineCoveredRuns
		out.ToolResultsMeasured += d.ToolResultsMeasured
		out.ToolResultsError += d.ToolResultsError
		out.AttemptTotal += d.AttemptTotal
		out.RetriedRuns += d.RetriedRuns
		out.AttributableFailedRuns += d.AttributableFailedRuns
		out.ExcludedFailedRuns += d.ExcludedFailedRuns
		out.FirstPassIssues += d.FirstPassIssues
		out.ReviewedIssues += d.ReviewedIssues
		for reason, n := range d.FailureReasonCounts {
			out.FailureReasonCounts[reason] += n
		}
		out.Deductions = append(out.Deductions, d.Deductions...)

		// D1's static half is a property of the version, not of the day. Every
		// day in a version's window carries the same number, so the first
		// non-nil one is the answer rather than a sum.
		if out.InjectedTokens == nil && d.InjectedTokens != nil {
			v := *d.InjectedTokens
			out.InjectedTokens = &v
		}

		if d.RunTokensMedian != nil && d.FinishedRuns > 0 {
			runTokens = append(runTokens, weighted{value: float64(*d.RunTokensMedian), weight: d.FinishedRuns})
		}
		if d.DisciplineScoreMedian != nil && d.DisciplineCoveredRuns > 0 {
			discipline = append(discipline, weighted{value: float64(*d.DisciplineScoreMedian), weight: d.DisciplineCoveredRuns})
		}
	}

	if v, ok := weightedMedian(runTokens); ok {
		n := int64(v)
		out.RunTokensMedian = &n
	}
	if v, ok := weightedMedian(discipline); ok {
		n := int(v)
		out.DisciplineScoreMedian = &n
	}
	return out
}

type weighted struct {
	value  float64
	weight int
}

// weightedMedian returns the value at the weight-weighted midpoint. With no
// samples it returns ok=false rather than 0 — the same rule the rest of the
// package follows.
func weightedMedian(in []weighted) (float64, bool) {
	if len(in) == 0 {
		return 0, false
	}
	sorted := make([]weighted, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].value < sorted[j].value })

	total := 0
	for _, w := range sorted {
		total += w.weight
	}
	half := float64(total) / 2
	seen := 0
	for _, w := range sorted {
		seen += w.weight
		if float64(seen) >= half {
			return w.value, true
		}
	}
	return sorted[len(sorted)-1].value, true
}
