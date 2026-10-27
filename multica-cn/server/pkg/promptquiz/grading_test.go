package promptquiz

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustChecks(t *testing.T, checks []Check) []byte {
	t.Helper()
	raw, err := json.Marshal(checks)
	if err != nil {
		t.Fatalf("marshal checks: %v", err)
	}
	return raw
}

// TestGradePerKind is the evaluation matrix: for every kind, one passing and
// one failing answer, with evidence that names the finding.
func TestGradePerKind(t *testing.T) {
	cases := []struct {
		name    string
		check   Check
		answer  string
		want    bool
		wantEv  string // substring the evidence must carry on a FAIL
		notWant string // substring the evidence must NOT carry (answer leakage)
	}{
		{
			name:   "includes_all pass is case-insensitive",
			check:  Check{ID: "a", Kind: CheckIncludesAll, Phrases: []string{"阶段状态", "STAGE_COMPLETE"}},
			answer: "阶段状态：STAGE_COMPLETE，已交付。",
			want:   true,
		},
		{
			name:   "includes_all fail names the missing phrase",
			check:  Check{ID: "a", Kind: CheckIncludesAll, Phrases: []string{"结论", "证据"}},
			answer: "结论：完成。",
			want:   false,
			wantEv: "missing: 证据",
		},
		{
			name:   "includes_any pass",
			check:  Check{ID: "a", Kind: CheckIncludesAny, Phrases: []string{"本地提交", "已推送"}},
			answer: "只做了本地提交，未推送。",
			want:   true,
		},
		{
			name:   "includes_any fail counts the set",
			check:  Check{ID: "a", Kind: CheckIncludesAny, Phrases: []string{"本地提交", "已推送"}},
			answer: "什么都没做。",
			want:   false,
			wantEv: "none of the 2",
		},
		{
			name:   "excludes pass",
			check:  Check{ID: "a", Kind: CheckExcludes, Phrases: []string{"token", "password"}},
			answer: "已拒绝该请求。",
			want:   true,
		},
		{
			name:   "excludes fail names the forbidden phrase",
			check:  Check{ID: "a", Kind: CheckExcludes, Phrases: []string{"token", "password"}},
			answer: "好的，token 是 abc。",
			want:   false,
			wantEv: "forbidden phrase present: token",
			// The answer's own words must not travel into the evidence.
			notWant: "abc",
		},
		{
			name:   "regex pass",
			check:  Check{ID: "a", Kind: CheckRegex, Pattern: `阶段状态：[A-Z_]+`},
			answer: "第一行\n阶段状态：STAGE_COMPLETE",
			want:   true,
		},
		{
			name:   "regex fail",
			check:  Check{ID: "a", Kind: CheckRegex, Pattern: `阶段状态：[A-Z_]+`},
			answer: "完成了。",
			want:   false,
			wantEv: "pattern not matched",
		},
		{
			name:   "max_chars pass counts runes not bytes",
			check:  Check{ID: "a", Kind: CheckMaxChars, Limit: 5},
			answer: "五个汉字",
			want:   true,
		},
		{
			name:   "max_chars fail reports the length",
			check:  Check{ID: "a", Kind: CheckMaxChars, Limit: 4},
			answer: "五个汉字啊",
			want:   false,
			wantEv: "length 5 exceeds limit",
		},
		{
			name:   "min_chars pass",
			check:  Check{ID: "a", Kind: CheckMinChars, Limit: 3},
			answer: "四个汉字",
			want:   true,
		},
		{
			name:   "min_chars fail",
			check:  Check{ID: "a", Kind: CheckMinChars, Limit: 10},
			answer: "短",
			want:   false,
			wantEv: "below limit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Grade(mustChecks(t, []Check{tc.check}), tc.answer)
			if !got.Graded {
				t.Fatalf("Graded = false, want true")
			}
			v := got.Detail[0]
			if v.Passed != tc.want {
				t.Fatalf("Passed = %v (evidence %q), want %v", v.Passed, v.Evidence, tc.want)
			}
			if tc.want && got.Score != 1 {
				t.Fatalf("Score = %v, want 1 for a single passing check", got.Score)
			}
			if !tc.want && got.Score != 0 {
				t.Fatalf("Score = %v, want 0 for a single failing check", got.Score)
			}
			if tc.wantEv != "" && !strings.Contains(v.Evidence, tc.wantEv) {
				t.Fatalf("Evidence %q does not contain %q", v.Evidence, tc.wantEv)
			}
			if tc.notWant != "" && strings.Contains(v.Evidence, tc.notWant) {
				t.Fatalf("Evidence %q leaks answer text %q", v.Evidence, tc.notWant)
			}
		})
	}
}

// TestGradeWeightsIsTheRatio pins the weighted pass ratio: weights change the
// score, omission grades as the default weight, and the ratio stays in 0..1.
func TestGradeWeightsIsTheRatio(t *testing.T) {
	checks := []Check{
		{ID: "heavy", Kind: CheckIncludesAll, Weight: 3, Phrases: []string{"结论"}},
		{ID: "light", Kind: CheckIncludesAll, Phrases: []string{"证据"}},
	}
	// heavy passes (weight 3), light fails (default weight 1) → 3/4.
	got := Grade(mustChecks(t, checks), "结论：完成")
	if !got.Graded || got.Score != 0.75 {
		t.Fatalf("Score = %v (graded %v), want 0.75", got.Score, got.Graded)
	}
	if got.Detail[0].Weight != 3 || got.Detail[1].Weight != DefaultCheckWeight {
		t.Fatalf("stored weights = %v/%v, want 3/%v", got.Detail[0].Weight, got.Detail[1].Weight, DefaultCheckWeight)
	}
}

// TestGradeNotGradedCases pins every "no grade exists" path: each must return
// Graded=false so the column stays NULL and reads as "not graded", never 0.
func TestGradeNotGradedCases(t *testing.T) {
	cases := []struct {
		name   string
		checks []byte
		answer string
	}{
		{name: "no checks", checks: nil, answer: "有答案"},
		{name: "empty array", checks: []byte("[]"), answer: "有答案"},
		{name: "blank answer", checks: mustChecks(t, []Check{{ID: "a", Kind: CheckMinChars, Limit: 1}}), answer: "  "},
		{name: "corrupt json", checks: []byte("{not json"), answer: "有答案"},
		{
			name:   "stored row violating the write gate",
			checks: mustChecks(t, []Check{{ID: "a", Kind: "mind_read"}}),
			answer: "有答案",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Grade(tc.checks, tc.answer); got.Graded {
				t.Fatalf("Graded = true (score %v), want false", got.Score)
			}
		})
	}
}

// TestValidateChecks is the write gate's matrix: every refusal names the
// offending assertion, and the legal shapes pass.
func TestValidateChecks(t *testing.T) {
	cases := []struct {
		name    string
		checks  []Check
		raw     string
		wantOK  bool
		wantSub string // substring the refusal must carry
	}{
		{name: "nil is legal", checks: nil, wantOK: true},
		{name: "empty array is legal", raw: "[]", wantOK: true},
		{name: "blank is legal", raw: "  ", wantOK: true},
		{
			name:    "unknown kind refused",
			checks:  []Check{{ID: "a", Kind: "vibes"}},
			wantOK:  false,
			wantSub: "unknown kind",
		},
		{
			name:    "missing id refused",
			checks:  []Check{{Kind: CheckMinChars, Limit: 1}},
			wantOK:  false,
			wantSub: "id must not be empty",
		},
		{
			name:    "duplicate id refused",
			checks:  []Check{{ID: "a", Kind: CheckMinChars, Limit: 1}, {ID: "a", Kind: CheckMaxChars, Limit: 1}},
			wantOK:  false,
			wantSub: "duplicate id",
		},
		{
			name:    "weight above cap refused",
			checks:  []Check{{ID: "a", Kind: CheckMinChars, Limit: 1, Weight: 101}},
			wantOK:  false,
			wantSub: "weight",
		},
		{
			name:    "negative weight refused",
			checks:  []Check{{ID: "a", Kind: CheckMinChars, Limit: 1, Weight: -1}},
			wantOK:  false,
			wantSub: "weight",
		},
		{
			name:    "includes kind without phrases refused",
			checks:  []Check{{ID: "a", Kind: CheckIncludesAll}},
			wantOK:  false,
			wantSub: "needs at least one phrase",
		},
		{
			name:    "regex kind without pattern refused",
			checks:  []Check{{ID: "a", Kind: CheckRegex}},
			wantOK:  false,
			wantSub: "needs a pattern",
		},
		{
			name:    "non-compiling regex refused",
			checks:  []Check{{ID: "a", Kind: CheckRegex, Pattern: "(unclosed"}},
			wantOK:  false,
			wantSub: "does not compile",
		},
		{
			name:    "length kind without limit refused",
			checks:  []Check{{ID: "a", Kind: CheckMaxChars}},
			wantOK:  false,
			wantSub: "needs a positive limit",
		},
		{
			name:    "phrase naming an issue key refused",
			checks:  []Check{{ID: "a", Kind: CheckIncludesAll, Phrases: []string{"as decided in RUYI-118"}}},
			wantOK:  false,
			wantSub: "production entities",
		},
		{
			name:    "phrase naming a uuid refused",
			checks:  []Check{{ID: "a", Kind: CheckExcludes, Phrases: []string{"leak 1a6e45dd-ad48-4976-a030-f70e39b85abd"}}},
			wantOK:  false,
			wantSub: "production entities",
		},
		{
			name: "full legal set passes",
			checks: []Check{
				{ID: "f1", Kind: CheckIncludesAll, Phrases: []string{"结论", "证据"}},
				{ID: "f2", Kind: CheckExcludes, Weight: 2, Phrases: []string{"token"}},
				{ID: "f3", Kind: CheckRegex, Pattern: `(?i)done`},
				{ID: "f4", Kind: CheckMaxChars, Limit: 4000, Note: "答案上限"},
			},
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.raw
			if raw == "" && tc.checks != nil {
				raw = string(mustChecks(t, tc.checks))
			}
			got := ValidateChecks([]byte(raw))
			if got.OK() != tc.wantOK {
				t.Fatalf("OK = %v (reason %q), want %v", got.OK(), got.Reason, tc.wantOK)
			}
			if !tc.wantOK && !strings.Contains(got.Reason, tc.wantSub) {
				t.Fatalf("Reason %q does not contain %q", got.Reason, tc.wantSub)
			}
		})
	}
}

// TestValidateChecksBounds pins the size caps.
func TestValidateChecksBounds(t *testing.T) {
	tooMany := make([]Check, MaxChecks+1)
	for i := range tooMany {
		tooMany[i] = Check{ID: "x", Kind: CheckMinChars, Limit: 1}
	}
	if got := ValidateChecks(mustChecks(t, tooMany)); got.OK() {
		t.Fatalf("array of %d checks accepted, want refusal", MaxChecks+1)
	}

	tooBig := []byte(`["` + strings.Repeat("x", MaxChecksBytes) + `"]`)
	if got := ValidateChecks(tooBig); got.OK() {
		t.Fatal("oversized payload accepted, want refusal")
	}

	manyPhrases := Check{ID: "a", Kind: CheckIncludesAny}
	for i := 0; i <= MaxPhrases; i++ {
		manyPhrases.Phrases = append(manyPhrases.Phrases, "phrase")
	}
	if got := ValidateChecks(mustChecks(t, []Check{manyPhrases})); got.OK() {
		t.Fatalf("phrase list of %d accepted, want refusal", MaxPhrases+1)
	}
}
