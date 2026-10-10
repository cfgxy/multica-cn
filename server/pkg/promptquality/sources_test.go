package promptquality

import "testing"

// T3: the dashboard may not depend on anything outside the platform. This is
// the assertion that makes that a rule rather than a claim — if someone ever
// marks an external source required, the seven cards become conditional on it
// and this fails.
func TestOnlyThePlatformsOwnDataIsRequired(t *testing.T) {
	for _, facts := range []SourceFacts{
		{Scoring: ScoringSource{Status: StatusOK}},
		{Scoring: ScoringSource{Status: StatusUnconfigured}},
		{Scoring: ScoringSource{Status: StatusError}},
		{Scoring: ScoringSource{Status: StatusDisabled}},
	} {
		for _, item := range DescribeSources(facts).Items {
			if item.Required && item.Kind != SourcePlatform {
				t.Errorf("%s is marked required; no external source may gate the dashboard", item.Kind)
			}
		}
	}
}

func TestLangfuseOffIsADegradedLabelNotAMissingSource(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	sources := DescribeSources(SourceFacts{Scoring: ScoringSource{Status: StatusOK, EffectiveSource: "deploy_default"}})
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

	sources := DescribeSources(SourceFacts{Scoring: ScoringSource{Status: StatusDisabled}})
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

// RUYI-551: the scoring source carries the four-state closed loop. The
// deploy-injected default never claims 配置错误 — it never promised a check
// (Plan A), so its status is ok with no validation data; only a module
// config with a recorded failed validation renders as error, and an error is
// not "degraded" (off) — it is configured and failing, which the banner
// renders differently.
func TestScoringStatusVocabularyFlowsThrough(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	cases := []struct {
		name        string
		scoring     ScoringSource
		available   bool
		degraded    bool
	}{
		{"module config ok", ScoringSource{Status: StatusOK, EffectiveSource: "module_config", Model: "ui-m", LastValidatedAt: "2026-10-08T00:00:00Z"}, true, false},
		{"deploy default ok", ScoringSource{Status: StatusOK, EffectiveSource: "deploy_default", Model: "env-m"}, true, false},
		{"unconfigured", ScoringSource{Status: StatusUnconfigured}, false, true},
		{"disabled", ScoringSource{Status: StatusDisabled, EffectiveSource: "module_config"}, false, true},
		{"validation error", ScoringSource{Status: StatusError, EffectiveSource: "module_config", ValidationError: "credentials rejected"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := DescribeSources(SourceFacts{Scoring: tc.scoring})
			scoring, ok := sources.ByKind(SourceScoring)
			if !ok {
				t.Fatal("scoring absent")
			}
			if scoring.Status != tc.scoring.Status || scoring.EffectiveSource != tc.scoring.EffectiveSource {
				t.Fatalf("status/source = %q/%q, want %q/%q", scoring.Status, scoring.EffectiveSource, tc.scoring.Status, tc.scoring.EffectiveSource)
			}
			if scoring.Available != tc.available || scoring.Degraded != tc.degraded {
				t.Fatalf("available/degraded = %v/%v, want %v/%v", scoring.Available, scoring.Degraded, tc.available, tc.degraded)
			}
			if scoring.Model != tc.scoring.Model || scoring.ValidationError != tc.scoring.ValidationError {
				t.Fatalf("model/validation_error = %q/%q", scoring.Model, scoring.ValidationError)
			}
			if scoring.LastValidatedAt != tc.scoring.LastValidatedAt {
				t.Fatalf("last_validated_at = %q", scoring.LastValidatedAt)
			}
		})
	}

	// The platform and langfuse sources are system-level: they carry the
	// vocabulary too, but nothing workspace-scoped.
	sources := DescribeSources(SourceFacts{Scoring: ScoringSource{Status: StatusOK}})
	if platform, _ := sources.ByKind(SourcePlatform); platform.Status != StatusOK || platform.EffectiveSource != SourceSystem {
		t.Fatalf("platform = %+v, want ok/system", platform)
	}
	langfuse, _ := sources.ByKind(SourceLangfuse)
	if langfuse.Status != StatusUnconfigured || langfuse.EffectiveSource != SourceSystem {
		t.Fatalf("langfuse = %+v, want unconfigured/system", langfuse)
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
