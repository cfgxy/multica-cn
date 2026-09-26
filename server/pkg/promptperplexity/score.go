// Package promptperplexity implements D3 of the prompt quality dashboard
// (RUYI-184): a rule-perplexity / ambiguity-risk score for one prompt version.
//
// It is deliberately NOT token-level perplexity. The Owner's Q17 ruling
// replaced that proxy with a direct reading: hand a model the fully assembled
// prompt a run actually receives, and have it judge — per sub-dimension —
// whether the layered rules can be followed literally and whether they
// contradict each other. The output is a band plus a percentage interval plus
// per-item evidence, never a bare number.
//
// Two rules shape the whole package:
//
//   - Scoring is per runtime profile. A plain member and a squad leader-task
//     receive different concatenations (the squad briefing is appended only on
//     the leader path), so they are scored separately and never averaged.
//   - A missing measurement is an error, not a zero. A response that omits a
//     sub-dimension is rejected rather than defaulted, because a 0 on this
//     dashboard reads as "scored badly", which the model never said.
package promptperplexity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Profile is the runtime shape a prompt was assembled for. The two values
// match prompt_perplexity_score.runtime_profile's CHECK constraint.
type Profile string

const (
	ProfileMember     Profile = "member"
	ProfileLeaderTask Profile = "leader_task"
)

// Profiles is every profile that gets its own score row.
var Profiles = []Profile{ProfileMember, ProfileLeaderTask}

// Band is the coarse verdict shown on the card. It matches
// prompt_perplexity_score.band's CHECK constraint.
type Band string

const (
	BandLow    Band = "low"
	BandMedium Band = "medium"
	BandHigh   Band = "high"
)

// MaxEvidenceFieldRunes caps every free-text evidence field. Evidence is meant
// to be a citation — which tier, which section — not an excerpt. The cap is
// what keeps a model that ignored that instruction from turning this column
// into a copy of the prompt body.
const MaxEvidenceFieldRunes = 120

// SubDimension is one scored aspect of a prompt.
type SubDimension struct {
	Key    string
	Label  string
	Weight float64
}

// SubDimensions is the seven-item list the drill-down sheet renders, in order.
//
// The first and last are fixed by the UI/UX spec (①注入层级正确性,
// ⑦角色规则完整性). The middle five are derived from the Q17 ruling's own
// wording — 分层组合与字面规则理解, 前后逻辑一致性, 逻辑冲突 — and are marked
// as such in the delivery note; they are this package's reading of the spec,
// not a quotation of it.
var SubDimensions = []SubDimension{
	{Key: "tier_placement", Label: "注入层级正确性", Weight: 0.15},
	{Key: "cross_tier_conflict", Label: "跨层规则冲突", Weight: 0.20},
	{Key: "literal_actionability", Label: "字面可执行性", Weight: 0.15},
	{Key: "internal_consistency", Label: "前后逻辑一致性", Weight: 0.15},
	{Key: "precedence_clarity", Label: "优先级裁决明确性", Weight: 0.15},
	{Key: "scope_boundary", Label: "职责边界清晰度", Weight: 0.10},
	{Key: "role_rule_completeness", Label: "角色规则完整性", Weight: 0.10},
}

// Evidence cites where a problem lives. It carries locators, never prompt
// text: tier name, section heading, and what the cited rule collides with.
type Evidence struct {
	Tier          string `json:"tier"`
	Section       string `json:"section"`
	ConflictsWith string `json:"conflicts_with,omitempty"`
	Note          string `json:"note,omitempty"`
}

// Item is one sub-dimension's verdict.
type Item struct {
	Key           string     `json:"key"`
	Score         float64    `json:"score"`
	Justification string     `json:"justification"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// Score is one validated model response, ready to be written as one
// prompt_perplexity_score row.
type Score struct {
	Band        Band    `json:"band"`
	PercentLow  float64 `json:"percent_low"`
	PercentHigh float64 `json:"percent_high"`
	Items       []Item  `json:"items"`
}

// ParseScore validates a raw GenerateJSON reply. Every rejection below is a
// case where storing the response would put a number on the dashboard that the
// model did not actually produce.
func ParseScore(raw string) (Score, error) {
	var s Score
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Score{}, fmt.Errorf("decode score: %w", err)
	}

	switch s.Band {
	case BandLow, BandMedium, BandHigh:
	default:
		return Score{}, fmt.Errorf("band %q is not one of low/medium/high", s.Band)
	}

	if s.PercentLow < 0 || s.PercentHigh > 100 {
		return Score{}, fmt.Errorf("interval %v–%v falls outside 0–100", s.PercentLow, s.PercentHigh)
	}
	// A zero-width interval claims a precision repeated scoring does not have.
	// The reproducibility claim this dimension makes IS the interval, so a
	// point estimate is a malformed claim rather than a confident one.
	if s.PercentHigh <= s.PercentLow {
		return Score{}, fmt.Errorf("interval %v–%v is not a widening range", s.PercentLow, s.PercentHigh)
	}

	byKey := make(map[string]Item, len(s.Items))
	for _, item := range s.Items {
		if _, ok := subDimensionByKey(item.Key); !ok {
			return Score{}, fmt.Errorf("sub-dimension %q is not in the declared list", item.Key)
		}
		if _, dup := byKey[item.Key]; dup {
			return Score{}, fmt.Errorf("sub-dimension %q scored twice", item.Key)
		}
		if item.Score < 0 || item.Score > 1 {
			return Score{}, fmt.Errorf("sub-dimension %q scored %v, outside 0–1", item.Key, item.Score)
		}
		byKey[item.Key] = item
	}

	// Ordered rebuild: the sheet renders these in spec order, and a missing
	// one is an error rather than a zero-filled row.
	ordered := make([]Item, 0, len(SubDimensions))
	for _, sd := range SubDimensions {
		item, ok := byKey[sd.Key]
		if !ok {
			return Score{}, fmt.Errorf("sub-dimension %q missing from the response", sd.Key)
		}
		item.Justification = truncateRunes(item.Justification, MaxEvidenceFieldRunes)
		for i := range item.Evidence {
			item.Evidence[i].Tier = truncateRunes(item.Evidence[i].Tier, MaxEvidenceFieldRunes)
			item.Evidence[i].Section = truncateRunes(item.Evidence[i].Section, MaxEvidenceFieldRunes)
			item.Evidence[i].ConflictsWith = truncateRunes(item.Evidence[i].ConflictsWith, MaxEvidenceFieldRunes)
			item.Evidence[i].Note = truncateRunes(item.Evidence[i].Note, MaxEvidenceFieldRunes)
		}
		ordered = append(ordered, item)
	}
	s.Items = ordered
	return s, nil
}

func subDimensionByKey(key string) (SubDimension, bool) {
	for _, sd := range SubDimensions {
		if sd.Key == key {
			return sd, true
		}
	}
	return SubDimension{}, false
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	// The ellipsis counts against the cap: max is what the column stores.
	return string(r[:max-1]) + "…"
}

// Tiers holds the prompt content of each injection layer, already read from
// prompt_version. An empty field means that layer was not injected.
type Tiers struct {
	Agent     string
	Workspace string
	Project   string
	Squad     string
}

// Assemble reproduces the concatenation a run of the given profile actually
// receives. The section order and headings mirror the daemon's brief builder
// (internal/daemon/execenv/runtime_config_sections.go) and the squad briefing
// append point (internal/handler/daemon.go); precedence in these prompts is
// stated by position, so a tidier order would score a document no run reads.
//
// An empty tier is omitted rather than rendered as an empty section: scoring a
// placeholder would score a layer that was never injected.
func Assemble(profile Profile, t Tiers) string {
	var b strings.Builder
	writeTier(&b, "Agent Identity", t.Agent)
	// The squad briefing is appended to the agent's instructions, and only on
	// the leader-task claim path. A plain member never reads this tier, so
	// including it here would report conflicts the member could not have seen.
	if profile == ProfileLeaderTask {
		writeTier(&b, "Squad Instructions", t.Squad)
	}
	writeTier(&b, "Workspace Context", t.Workspace)
	writeTier(&b, "Project Instructions", t.Project)
	return b.String()
}

func writeTier(b *strings.Builder, heading, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	b.WriteString(content)
	b.WriteString("\n\n")
}
