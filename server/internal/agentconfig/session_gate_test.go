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
			name:          "threshold is exact for a ceiling that is not a multiple of 100",
			contextTokens: 74,
			known:         true,
			maxTokens:     101,
			compactPct:    75,
			want:          SessionResumeAllowed,
		},
		{
			name:          "threshold is exact for a ceiling that is not a multiple of 100 (boundary)",
			contextTokens: 75,
			known:         true,
			maxTokens:     101,
			compactPct:    75,
			want:          SessionResumeCompactSoft,
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
