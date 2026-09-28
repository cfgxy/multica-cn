package promptquiz

import "testing"

func TestDiscriminationFor(t *testing.T) {
	cases := []struct {
		name     string
		attempts int
		graded   int
		median   float64
		iqr      float64
		want     Discrimination
	}{
		{"never asked", 0, 0, 0, 0, DiscriminationPending},
		{"asked twice, both answered", 2, 2, 1000, 500, DiscriminationPending},
		{"asked four times, one short of a verdict", 4, 4, 1000, 500, DiscriminationPending},
		// The floor case: asked enough, never produced a readable answer. Reported
		// as no_signal rather than pending, because "not asked often enough" would
		// invite waiting for a sample that is never going to arrive.
		{"asked enough, nothing readable", 8, 0, 0, 0, DiscriminationNoSignal},
		// Mostly errored but a few readings: still pending on the graded count,
		// which is the count the spread is computed from.
		{"asked enough, only two readable", 9, 2, 1000, 500, DiscriminationPending},
		{"identical readings", 6, 6, 1000, 0, DiscriminationFlat},
		{"spread just inside the flat band", 6, 6, 1000, FlatIQRRatio * 1000, DiscriminationFlat},
		{"spread just outside the flat band", 6, 6, 1000, FlatIQRRatio*1000 + 1, DiscriminationOK},
		// Every answer cost nothing measurable: no level to take a ratio against.
		{"zero median", 6, 6, 0, 0, DiscriminationFlat},
		// The quietest real distribution this deployment has measured
		// (IQR/median = 0.249, see baseline.go) must read as a working question.
		{"the calibration sample's own spread", 31, 31, 143954, 35852, DiscriminationOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiscriminationFor(c.attempts, c.graded, c.median, c.iqr)
			if got != c.want {
				t.Errorf("DiscriminationFor(attempts=%d, graded=%d, median=%g, iqr=%g) = %q, want %q",
					c.attempts, c.graded, c.median, c.iqr, got, c.want)
			}
		})
	}
}

// The threshold is only defensible if it sits well below the quietest real
// distribution on record; a threshold above it would mark live questions as
// flat, and deleting a working question is the expensive error here.
func TestFlatIQRRatioStaysBelowTheMeasuredDispersion(t *testing.T) {
	q := quantile(measuredSample, 0.75) - quantile(measuredSample, 0.25)
	measured := q / quantile(measuredSample, 0.5)
	if measured <= FlatIQRRatio*5 {
		t.Errorf("measured IQR/median = %.3f is not at least 5x FlatIQRRatio (%.3f); the flat band now risks marking a real question",
			measured, FlatIQRRatio)
	}
}
