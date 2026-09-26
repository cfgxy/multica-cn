package promptperplexity

import (
	"strings"
	"testing"
)

func validScore() string {
	return `{
      "band": "medium",
      "percent_low": 40,
      "percent_high": 55,
      "items": [
        {"key": "tier_placement", "score": 0.8, "justification": "workspace and agent tiers do not overlap"},
        {"key": "cross_tier_conflict", "score": 0.5, "justification": "two tiers disagree on stash discipline",
         "evidence": [{"tier": "workspace", "section": "团队纪律", "conflicts_with": "agent:执行前自检"}]},
        {"key": "literal_actionability", "score": 0.6, "justification": "several rules name no concrete command"},
        {"key": "internal_consistency", "score": 0.7, "justification": "ordering rules agree"},
        {"key": "precedence_clarity", "score": 0.9, "justification": "precedence is stated once"},
        {"key": "scope_boundary", "score": 0.4, "justification": "two tiers claim the same boundary"},
        {"key": "role_rule_completeness", "score": 0.6, "justification": "one role lacks a stop condition"}
      ]
    }`
}

func TestParseScoreAcceptsACompleteResponse(t *testing.T) {
	got, err := ParseScore(validScore())
	if err != nil {
		t.Fatalf("ParseScore: %v", err)
	}
	if got.Band != BandMedium {
		t.Errorf("Band = %q, want medium", got.Band)
	}
	if got.PercentLow != 40 || got.PercentHigh != 55 {
		t.Errorf("interval = %v–%v, want 40–55", got.PercentLow, got.PercentHigh)
	}
	if len(got.Items) != len(SubDimensions) {
		t.Errorf("Items = %d, want %d", len(got.Items), len(SubDimensions))
	}
}

// A response missing a sub-dimension is not a partial score to be filled with
// zeros: a zero there reads on the dashboard as "this prompt scored badly on
// tier placement", which is a claim the model never made.
func TestParseScoreRejectsAMissingSubDimension(t *testing.T) {
	body := strings.Replace(validScore(),
		`{"key": "scope_boundary", "score": 0.4, "justification": "two tiers claim the same boundary"},`, "", 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted a response with only six sub-dimensions")
	}
}

func TestParseScoreRejectsAnUnknownSubDimension(t *testing.T) {
	body := strings.Replace(validScore(), `"tier_placement"`, `"vibes"`, 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted a sub-dimension outside the declared list")
	}
}

func TestParseScoreRejectsOutOfRangeScores(t *testing.T) {
	body := strings.Replace(validScore(), `"score": 0.8`, `"score": 1.4`, 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted a sub-dimension score above 1")
	}
}

func TestParseScoreRejectsAnInvertedInterval(t *testing.T) {
	body := strings.Replace(validScore(), `"percent_high": 55`, `"percent_high": 20`, 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted percent_high below percent_low")
	}
}

// A point estimate implies a precision repeated scoring does not have. The
// reproducibility claim is the band plus the interval, so a zero-width
// interval is a malformed claim rather than a confident one.
func TestParseScoreRejectsAZeroWidthInterval(t *testing.T) {
	body := strings.Replace(validScore(), `"percent_high": 55`, `"percent_high": 40`, 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted a zero-width reproducibility interval")
	}
}

func TestParseScoreRejectsAnUnknownBand(t *testing.T) {
	body := strings.Replace(validScore(), `"band": "medium"`, `"band": "catastrophic"`, 1)
	if _, err := ParseScore(body); err == nil {
		t.Fatal("ParseScore accepted a band outside low/medium/high")
	}
}

// The model is told to cite where a rule lives, not to quote it. An evidence
// item that came back carrying a long excerpt is dropped rather than stored:
// this row is read by the dashboard, and prompt bodies do not leave the
// platform's own tables through it.
func TestParseScoreTruncatesOverlongEvidenceFields(t *testing.T) {
	long := strings.Repeat("规", 400)
	body := strings.Replace(validScore(), `"section": "团队纪律"`, `"section": "`+long+`"`, 1)
	got, err := ParseScore(body)
	if err != nil {
		t.Fatalf("ParseScore: %v", err)
	}
	for _, item := range got.Items {
		for _, ev := range item.Evidence {
			if len([]rune(ev.Section)) > MaxEvidenceFieldRunes {
				t.Errorf("evidence section kept %d runes, want at most %d", len([]rune(ev.Section)), MaxEvidenceFieldRunes)
			}
		}
	}
}

func TestParseScoreRejectsNonJSON(t *testing.T) {
	if _, err := ParseScore("I'm afraid I can't score that."); err == nil {
		t.Fatal("ParseScore accepted a non-JSON response")
	}
}

// ---------------------------------------------------------------- assembly

// The order is the daemon's, not a tidier one: Agent Identity, then Workspace
// Context, then Project Instructions (internal/daemon/execenv/
// runtime_config_sections.go). Precedence in these prompts is stated by
// position, so scoring a reordered concatenation scores a different document.
func TestAssembleFollowsTheDaemonsSectionOrder(t *testing.T) {
	got := Assemble(ProfileMember, Tiers{
		Workspace: "W", Project: "P", Squad: "S", Agent: "A",
	})
	ia, iw, ip := strings.Index(got, "\nA"), strings.Index(got, "\nW"), strings.Index(got, "\nP")
	if ia < 0 || iw < 0 || ip < 0 {
		t.Fatalf("assembled prompt dropped a tier:\n%s", got)
	}
	if !(ia < iw && iw < ip) {
		t.Errorf("assembled order is agent=%d workspace=%d project=%d, want agent → workspace → project:\n%s", ia, iw, ip, got)
	}
}

// An empty tier is not injected at runtime, so scoring a placeholder for it
// would score a prompt no run ever saw.
func TestAssembleOmitsEmptyTiers(t *testing.T) {
	got := Assemble(ProfileMember, Tiers{Workspace: "W", Agent: "A"})
	if strings.Contains(got, "Project Instructions") || strings.Contains(got, "Squad Instructions") {
		t.Errorf("assembled prompt invented a tier the run never saw:\n%s", got)
	}
}

// The two runtime profiles are scored separately and never merged (Owner Q17).
// The concrete reason they differ: the squad briefing is appended to the
// agent's instructions only on the leader-task claim path
// (internal/handler/daemon.go), so a plain member never reads the squad tier.
// Scoring one concatenation for both would report conflicts against a member
// who could not have seen the conflicting rule.
func TestAssembleGivesSquadTierOnlyToLeaderTask(t *testing.T) {
	tiers := Tiers{Workspace: "W", Squad: "S", Agent: "A"}
	member := Assemble(ProfileMember, tiers)
	leader := Assemble(ProfileLeaderTask, tiers)
	if strings.Contains(member, "\nS") {
		t.Errorf("member profile carries the squad tier it never receives at runtime:\n%s", member)
	}
	if !strings.Contains(leader, "\nS") {
		t.Errorf("leader_task profile dropped the squad tier:\n%s", leader)
	}
}

func TestProfilesAreTheTwoTheSchemaAccepts(t *testing.T) {
	if len(Profiles) != 2 {
		t.Fatalf("Profiles = %v, want exactly member and leader_task", Profiles)
	}
	for _, p := range Profiles {
		if p != ProfileMember && p != ProfileLeaderTask {
			t.Errorf("unknown profile %q: prompt_perplexity_score.runtime_profile would reject it", p)
		}
	}
}
