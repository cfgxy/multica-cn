package agentconfig

import "testing"

func TestDecideSessionResume(t *testing.T) {
	const cap400k = DefaultSessionMaxContextTokens

	tests := []struct {
		name          string
		contextTokens int64
		known         bool
		maxTokens     int64
		compactPct    int32
		want          SessionResumeDecision
	}{
		{
			name:          "gate disabled resumes even when far past the default ceiling",
			contextTokens: 5_000_000,
			known:         true,
			maxTokens:     DisabledSessionMaxContextTokens,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeGateDisabled,
		},
		{
			name:          "negative ceiling is treated as disabled, never as an inverted gate",
			contextTokens: 1,
			known:         true,
			maxTokens:     -1,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeGateDisabled,
		},
		{
			name:          "unknown reading resumes (D4 A)",
			contextTokens: 0,
			known:         false,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeSizeUnknown,
		},
		{
			name:          "a zero reading is unknown, not an empty conversation",
			contextTokens: 0,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeSizeUnknown,
		},
		{
			name:          "one token below the soft threshold still resumes",
			contextTokens: 319_999,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeAllowed,
		},
		{
			name:          "exactly at the soft threshold compacts",
			contextTokens: 320_000,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeCompactSoft,
		},
		{
			name:          "one token below the ceiling is still the soft case",
			contextTokens: cap400k - 1,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeCompactSoft,
		},
		{
			name:          "exactly at the ceiling is the hard case",
			contextTokens: cap400k,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeCompactHard,
		},
		{
			name:          "100 percent compacts only at the ceiling",
			contextTokens: cap400k - 1,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    100,
			want:          SessionResumeAllowed,
		},
		{
			name:          "out-of-range percentage falls back to the default rather than clamping",
			contextTokens: 200_000,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    1,
			want:          SessionResumeAllowed,
		},
		{
			name:          "out-of-range percentage still compacts once the default threshold is met",
			contextTokens: 320_000,
			known:         true,
			maxTokens:     cap400k,
			compactPct:    0,
			want:          SessionResumeCompactSoft,
		},
		{
			// 700001*75/100 = 525000 exactly; dividing first would give
			// 700001/100 = 7000 and a threshold 75 tokens low.
			name:          "threshold is exact for a ceiling that is not a multiple of 100",
			contextTokens: 524_999,
			known:         true,
			maxTokens:     700_001,
			compactPct:    75,
			want:          SessionResumeAllowed,
		},
		{
			name:          "threshold is exact for a ceiling that is not a multiple of 100 (boundary)",
			contextTokens: 525_000,
			known:         true,
			maxTokens:     700_001,
			compactPct:    75,
			want:          SessionResumeCompactSoft,
		},
		{
			// The frozen spec's interlock. 100_000 at 10% works out to 10_000,
			// under one turn's prompt; without the floor the agent would start
			// every single run from a brief.
			name:          "the smallest legal ceiling at the smallest legal percentage does not compact below 50K",
			contextTokens: 49_999,
			known:         true,
			maxTokens:     MinSessionMaxContextTokens,
			compactPct:    MinSessionCompactPct,
			want:          SessionResumeAllowed,
		},
		{
			name:          "the 50K floor is where that configuration actually compacts",
			contextTokens: MinEffectiveCompactThreshold,
			known:         true,
			maxTokens:     MinSessionMaxContextTokens,
			compactPct:    MinSessionCompactPct,
			want:          SessionResumeCompactSoft,
		},
		{
			// The floor must not overtake a percentage that is already higher:
			// 2M at 80% is 1.6M, and flooring must leave it there.
			name:          "the floor does not lower a threshold that is already above it",
			contextTokens: 1_599_999,
			known:         true,
			maxTokens:     MaxSessionMaxContextTokens,
			compactPct:    DefaultSessionCompactPct,
			want:          SessionResumeAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideSessionResume(tt.contextTokens, tt.known, tt.maxTokens, tt.compactPct)
			if got != tt.want {
				t.Fatalf("DecideSessionResume(%d, %t, %d, %d) = %q, want %q",
					tt.contextTokens, tt.known, tt.maxTokens, tt.compactPct, got, tt.want)
			}
		})
	}
}

func TestSessionResumeDecisionShouldStartFreshSession(t *testing.T) {
	fresh := map[SessionResumeDecision]bool{
		SessionResumeAllowed:      false,
		SessionResumeGateDisabled: false,
		SessionResumeSizeUnknown:  false,
		SessionResumeCompactSoft:  true,
		SessionResumeCompactHard:  true,
	}
	for decision, want := range fresh {
		if got := decision.ShouldStartFreshSession(); got != want {
			t.Errorf("%q.ShouldStartFreshSession() = %t, want %t", decision, got, want)
		}
	}
}

func TestValidateSessionMaxContextTokens(t *testing.T) {
	valid := []int64{0, MinSessionMaxContextTokens, DefaultSessionMaxContextTokens, MaxSessionMaxContextTokens}
	for _, v := range valid {
		if err := ValidateSessionMaxContextTokens(v); err != nil {
			t.Errorf("ValidateSessionMaxContextTokens(%d) = %v, want nil", v, err)
		}
	}
	// 1..Min-1 is rejected rather than silently rounded: a ceiling that small
	// would compact on the very first turn, which is indistinguishable from a
	// broken agent.
	invalid := []int64{-1, 1, MinSessionMaxContextTokens - 1, MaxSessionMaxContextTokens + 1}
	for _, v := range invalid {
		if err := ValidateSessionMaxContextTokens(v); err == nil {
			t.Errorf("ValidateSessionMaxContextTokens(%d) = nil, want error", v)
		}
	}
}

func TestValidateSessionCompactPct(t *testing.T) {
	for _, v := range []int32{MinSessionCompactPct, DefaultSessionCompactPct, MaxSessionCompactPct} {
		if err := ValidateSessionCompactPct(v); err != nil {
			t.Errorf("ValidateSessionCompactPct(%d) = %v, want nil", v, err)
		}
	}
	for _, v := range []int32{-1, 0, MinSessionCompactPct - 1, MaxSessionCompactPct + 1} {
		if err := ValidateSessionCompactPct(v); err == nil {
			t.Errorf("ValidateSessionCompactPct(%d) = nil, want error", v)
		}
	}
}

func TestEffectiveCompactThreshold(t *testing.T) {
	tests := []struct {
		name       string
		maxTokens  int64
		compactPct int32
		want       int64
	}{
		{"default configuration", DefaultSessionMaxContextTokens, DefaultSessionCompactPct, 320_000},
		{"floored when the arithmetic lands under 50K", MinSessionMaxContextTokens, MinSessionCompactPct, MinEffectiveCompactThreshold},
		{"exact for a ceiling that is not a multiple of 100", 700_001, 75, 525_000},
		{"an invalid percentage uses the default, not a clamp", DefaultSessionMaxContextTokens, 0, 320_000},
		{
			// Only reachable from a row written before the range existed. The
			// floor must not push the soft switch past the hard ceiling, which
			// would leave that agent with no soft stage at all.
			name: "the floor never exceeds the ceiling it protects", maxTokens: 20_000, compactPct: 50, want: 20_000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveCompactThreshold(tt.maxTokens, tt.compactPct); got != tt.want {
				t.Fatalf("EffectiveCompactThreshold(%d, %d) = %d, want %d",
					tt.maxTokens, tt.compactPct, got, tt.want)
			}
		})
	}
}

// TestSessionMaxContextTokensMatchesFrozenSpec pins the range to the numbers
// the product froze (0, or [100_000, 2_000_000]) rather than to whatever the
// constants happen to say, so widening them is a deliberate edit here too.
func TestSessionMaxContextTokensMatchesFrozenSpec(t *testing.T) {
	if MinSessionMaxContextTokens != 100_000 || MaxSessionMaxContextTokens != 2_000_000 {
		t.Fatalf("frozen range is 0 or [100000, 2000000], got [%d, %d]",
			MinSessionMaxContextTokens, MaxSessionMaxContextTokens)
	}
	if MinEffectiveCompactThreshold != 50_000 {
		t.Fatalf("frozen effective-trigger floor is 50000, got %d", MinEffectiveCompactThreshold)
	}
	for _, v := range []int64{10_000, 99_999, 2_000_001, 10_000_000} {
		if err := ValidateSessionMaxContextTokens(v); err == nil {
			t.Errorf("ValidateSessionMaxContextTokens(%d) = nil, want error (outside the frozen range)", v)
		}
	}
}
