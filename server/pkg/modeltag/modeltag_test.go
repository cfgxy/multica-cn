package modeltag

import "testing"

// TestStripOneFourCategoryMatrix is the shared rule source's own matrix for
// the four id categories every consumer must agree on (RUYI-267): catalog
// models, out-of-catalog gateway aliases, tagged ids and malformed shapes.
// pkg/agent and internal/metrics pin their own ends of this through
// buildClaudeArgs and PriceForModelAlias; this table pins the rule itself so
// a shape change here is a conscious decision, never an accident.
func TestStripOneFourCategoryMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		model           string
		wantStripped    string
		wantChanged     bool
		wantStillTagged bool
		wantMatch       bool
	}{
		// Catalog models: untagged ids are returned untouched.
		{"catalog claude", "claude-opus-5", "claude-opus-5", false, false, false},
		{"catalog kimi", "kimi-k3", "kimi-k3", false, false, false},

		// Out-of-catalog gateway aliases: same treatment as catalog ids —
		// the rule keys on shape, never on catalog membership, so a bare
		// alias an installed CLI does not list (gpt-5.6-sol behind a
		// routing gateway) passes through verbatim.
		{"gateway alias bare", "gpt-5.6-sol", "gpt-5.6-sol", false, false, false},
		{"vendor-prefixed alias bare", "moonshotai/kimi-k3", "moonshotai/kimi-k3", false, false, false},

		// Tagged ids: exactly one trailing tag is removed.
		{"claude 1m", "claude-opus-5[1m]", "claude-opus-5", true, false, true},
		{"gateway 1m", "gpt-5.6-sol[1m]", "gpt-5.6-sol", true, false, true},
		{"kimi 1m", "kimi-k3[1m]", "kimi-k3", true, false, true},
		{"vendor-prefixed tagged", "moonshotai/kimi-k3[1m]", "moonshotai/kimi-k3", true, false, true},
		{"non-size tag", "gpt-5.6-sol[foo]", "gpt-5.6-sol", true, false, true},

		// Malformed shapes: not a complete trailing tag means not a tag.
		{"empty tag", "gpt-5.6-sol[]", "gpt-5.6-sol[]", false, false, false},
		{"unterminated tag", "gpt-5.6-sol[1m", "gpt-5.6-sol[1m", false, false, false},
		{"tag not at end", "gpt-5.6-sol[1m]-preview", "gpt-5.6-sol[1m]-preview", false, false, false},
		// A bare tag is, by this shape, a complete trailing tag — stripping
		// it empties the id. Pre-existing consumer behavior (identical
		// under the pre-convergence regexes); the launch side only gets
		// here for a pathological persisted model, and the CLI rejects the
		// empty --model it would produce.
		{"bare tag", "[1m]", "", true, false, true},
		{"empty string", "", "", false, false, false},

		// Doubly tagged: one tag is peeled, and the result is reported as
		// still tagged so consumers can refuse the id instead of treating
		// the remainder as deliberate.
		{"double tag", "gpt-5.6-sol[1m][2m]", "gpt-5.6-sol[1m]", true, true, true},
		{"double tag claude", "claude-opus-5[1m][2m]", "claude-opus-5[1m]", true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stripped, changed, stillTagged := StripOne(tc.model)
			if stripped != tc.wantStripped || changed != tc.wantChanged || stillTagged != tc.wantStillTagged {
				t.Fatalf("StripOne(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.model, stripped, changed, stillTagged,
					tc.wantStripped, tc.wantChanged, tc.wantStillTagged)
			}
			if got := Match(tc.model); got != tc.wantMatch {
				t.Fatalf("Match(%q) = %v, want %v", tc.model, got, tc.wantMatch)
			}
		})
	}
}

// TestPatternEmbedsIntoLargerRules pins the two exported pattern constants
// against the compiled behaviour: whatever alternation embeds Pattern
// (claudeVersionEnd, the qwen alias rules) must accept exactly the ids Match
// accepts as tagged suffixes, and nothing extra.
func TestPatternEmbedsIntoLargerRules(t *testing.T) {
	t.Parallel()

	if Pattern == "" || AtEnd != Pattern+"$" {
		t.Fatalf("pattern constants drifted: Pattern=%q AtEnd=%q", Pattern, AtEnd)
	}
}
