package promptquality

import "testing"

// T3: the dashboard may not depend on anything outside the platform. This is
// the assertion that makes that a rule rather than a claim — if someone ever
// marks an external source required, the seven cards become conditional on it
// and this fails.
func TestOnlyThePlatformsOwnDataIsRequired(t *testing.T) {
	for _, scoring := range []bool{true, false} {
		for _, item := range DescribeSources(scoring).Items {
			if item.Required && item.Kind != SourcePlatform {
				t.Errorf("%s is marked required; no external source may gate the dashboard", item.Kind)
			}
		}
	}
}

func TestLangfuseOffIsADegradedLabelNotAMissingSource(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	sources := DescribeSources(true)
	langfuse, ok := sources.ByKind(SourceLangfuse)
	if !ok {
		t.Fatal("langfuse absent from the source list; the footer cannot say it is off")
	}
	if langfuse.Available {
		t.Error("langfuse reported available with no keys configured")
	}
	if !langfuse.Degraded {
		t.Error("langfuse off must raise the degraded flag the footer renders")
	}
	if !sources.Degraded() {
		t.Error("window not degraded with an optional source off")
	}

	// The point of the flag: nothing else moved.
	platform, _ := sources.ByKind(SourcePlatform)
	if !platform.Available || platform.Degraded {
		t.Error("turning langfuse off changed the platform source's state")
	}
	scoring, _ := sources.ByKind(SourceScoring)
	if !scoring.Available {
		t.Error("turning langfuse off changed the scoring model's state")
	}
}

// Both keys are needed: a half-configured export is not an export, and
// reporting it as available would put the dashboard's footer at odds with what
// the exporter can actually do.
func TestLangfuseNeedsBothKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-present")
	t.Setenv("LANGFUSE_SECRET_KEY", "  ")
	if LangfuseExportEnabled() {
		t.Error("export reported enabled with only one key")
	}

	t.Setenv("LANGFUSE_SECRET_KEY", "sk-present")
	if !LangfuseExportEnabled() {
		t.Error("export reported disabled with both keys set")
	}
}

func TestScoringModelOffDegradesOnItsOwn(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-present")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-present")

	sources := DescribeSources(false)
	scoring, ok := sources.ByKind(SourceScoring)
	if !ok {
		t.Fatal("scoring model absent from the source list")
	}
	if scoring.Available || !scoring.Degraded {
		t.Errorf("scoring model = %+v, want unavailable and degraded", scoring)
	}
	if langfuse, _ := sources.ByKind(SourceLangfuse); langfuse.Degraded {
		t.Error("an unconfigured scoring model degraded langfuse too")
	}
}

// Every dimension keeps working with both optional sources off: the seven
// measures come out of Result, which DescribeSources does not touch. D3 is the
// exception by design — it is the one dimension the scoring model produces, and
// it is reported as unscored rather than as a neutral band.
func TestPresentationIsUnaffectedByOptionalSources(t *testing.T) {
	r := Result{
		FinishedRuns:        12,
		ToolResultsMeasured: 40,
		ToolResultsError:    4,
		RetriedRuns:         3,
		FirstPassIssues:     5,
		ReviewedIssues:      8,
	}
	want := Present(r)

	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	got := Present(r)

	if got.ToolFailureRate.State != want.ToolFailureRate.State {
		t.Errorf("D4 state changed with langfuse off: %s", got.ToolFailureRate.State)
	}
	if got.ToolFailureRate.Value == nil || *got.ToolFailureRate.Value != *want.ToolFailureRate.Value {
		t.Error("D4 value changed with langfuse off")
	}
	if got.RetryRate.State != StateOK || got.FirstPassRate.State != StateOK {
		t.Errorf("D5/D7 degraded with langfuse off: %s / %s", got.RetryRate.State, got.FirstPassRate.State)
	}
}
