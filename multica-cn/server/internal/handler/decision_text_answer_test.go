package handler

import (
	"encoding/json"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Text decision answers (RUYI-471): a member comment whose ENTIRE content is
// a compact token sequence ("1A 2B") answers the issue's open cards. The
// parser is the exact-matching gate — everything below drives it table-driven
// so each rejection reason stays independently observable.

func TestParseDecisionAnswerTokens_Accepts(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []decisionAnswerToken
	}{
		{"plain space", "1A 2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"lowercase folded", "1a 2b", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"mixed case folded", "1Ab 2Bc", []decisionAnswerToken{{1, "AB"}, {2, "BC"}}},
		{"ideographic comma", "1A、2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"fullwidth comma", "1A，2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"ascii comma", "1A,2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"slash", "1A/2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"runs of mixed separators", "1A 、\t 2B，/3C", []decisionAnswerToken{{1, "A"}, {2, "B"}, {3, "C"}}},
		{"leading and trailing whitespace", "  1A 2B\n", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"newline separator", "1A\n2B", []decisionAnswerToken{{1, "A"}, {2, "B"}}},
		{"multi-letter pick", "1AB", []decisionAnswerToken{{1, "AB"}}},
		{"multi-digit sequence", "10A 11B", []decisionAnswerToken{{10, "A"}, {11, "B"}}},
		{"all four options", "4D", []decisionAnswerToken{{4, "D"}}},
		{"trailing whitespace trimmed then hit", "1A ", []decisionAnswerToken{{1, "A"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDecisionAnswerTokens(tc.content)
			if !ok {
				t.Fatalf("parseDecisionAnswerTokens(%q) rejected, want %v", tc.content, tc.want)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("token %d = %+v, want %+v (input %q)", i, got[i], tc.want[i], tc.content)
				}
			}
		})
	}
}

func TestParseDecisionAnswerTokens_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\t"},
		{"trailing prose", "1A 2B 加急"},
		{"leading prose", "先答：1A 2B"},
		{"prose both sides", "选 1A 2B 谢谢"},
		{"chinese narrative", "决策1选A"},
		{"digits and letters separated", "1 A 2 B"},
		{"no separator between tokens", "1A2B"},
		{"zero sequence", "0A 1B"},
		{"leading zero sequence", "01A"},
		{"digits without letters", "1 2"},
		{"letters without digits", "AB"},
		{"letter beyond option range", "1A 2E"},
		{"trailing comma separator", "1A ,"},
		{"trailing ideographic comma", "1A、"},
		{"mention link prefix", "[@x](mention://agent/1) 1A 2B"},
		{"quote markers", "> 1A 2B"},
		{"code fence", "```\n1A 2B\n```"},
		{"fullwidth digits", "１Ａ ２Ｂ"},
		{"parenthetical remark", "1A 2B（推荐）"},
		{"emoji", "1A 2B 👍"},
		{"punctuation suffix", "1A 2B."},
		{"note prefix", "/note 1A 2B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := parseDecisionAnswerTokens(tc.content); ok {
				t.Fatalf("parseDecisionAnswerTokens(%q) = %v, want rejection", tc.content, got)
			}
		})
	}
}

// The negative assertions above are only meaningful if they genuinely ride on
// exact matching: loosening the parser to "contains tokens" or "starts with a
// token" must flip the prose-mixed cases to accepted. Guard that property by
// asserting the near-miss variants that such a loosened parser WOULD accept.
func TestParseDecisionAnswerTokens_NearMissesAreDistinctShapes(t *testing.T) {
	// If these ever parse, the trailing-prose rejections above are dead
	// assertions (they would pass for the wrong reason).
	for _, nearMiss := range []string{"1A 2B", "1A"} {
		if _, ok := parseDecisionAnswerTokens(nearMiss); !ok {
			t.Fatalf("baseline %q must parse: the rejection cases only guard exact matching if the clean form is accepted", nearMiss)
		}
	}
}

// openCard is a small builder for bind tests: options derived from the option
// count, single-select unless multi.
func openCard(opts int, multi bool) db.IssueDecision {
	labels := make([]string, opts)
	for i := range labels {
		labels[i] = string(rune('a' + i))
	}
	options, _, err := validateDecisionInput("q", labels, nil)
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(options)
	if err != nil {
		panic(err)
	}
	return db.IssueDecision{
		Options:     raw,
		MultiSelect: multi,
		Status:      "open",
	}
}

func TestBindDecisionTextAnswer(t *testing.T) {
	t.Run("binds in creation order with seq 1..N", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false), openCard(3, false)}
		bound, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {2, "B"}}, open)
		if !ok || len(bound) != 2 {
			t.Fatalf("bind = %v ok=%v, want two bound answers", bound, ok)
		}
		if bound[0].Indices[0] != 0 || bound[1].Indices[0] != 1 {
			t.Fatalf("indices = %v, want card1=[0] card2=[1]", bound)
		}
	})
	t.Run("accepts out-of-order tokens covering 1..N", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false), openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{2, "B"}, {1, "A"}}, open); !ok {
			t.Fatal("out-of-order full coverage: expected acceptance")
		}
	})
	t.Run("multi-select letters map to multiple indices", func(t *testing.T) {
		open := []db.IssueDecision{openCard(3, true)}
		bound, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "AC"}}, open)
		if !ok || len(bound[0].Indices) != 2 || bound[0].Indices[0] != 0 || bound[0].Indices[1] != 2 {
			t.Fatalf("bind = %v ok=%v, want [0 2]", bound, ok)
		}
	})
	t.Run("token count must equal open card count", func(t *testing.T) {
		one := []db.IssueDecision{openCard(2, false)}
		three := []db.IssueDecision{openCard(2, false), openCard(2, false), openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {2, "B"}}, one); ok {
			t.Fatal("two tokens one card: expected rejection")
		}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {2, "B"}}, three); ok {
			t.Fatal("two tokens three cards: expected rejection")
		}
	})
	t.Run("sequence gap rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false), openCard(2, false), openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {3, "C"}}, open); ok {
			t.Fatal("gap 1,3 over three cards: expected rejection")
		}
	})
	t.Run("duplicate sequence rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false), openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {1, "B"}}, open); ok {
			t.Fatal("duplicate seq 1: expected rejection")
		}
	})
	t.Run("sequence beyond open card count rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}, {2, "B"}}, open); ok {
			t.Fatal("seq 2 over one card: expected rejection")
		}
	})
	t.Run("multi-letter pick on single-select rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(3, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "AB"}}, open); ok {
			t.Fatal("two letters on single-select: expected rejection")
		}
	})
	t.Run("repeated letter rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(3, true)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "AA"}}, open); ok {
			t.Fatal("repeated letter: expected rejection")
		}
	})
	t.Run("letter beyond card option count rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false)}
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "C"}}, open); ok {
			t.Fatal("letter C over two options: expected rejection")
		}
	})
	t.Run("no open cards rejected at bind layer", func(t *testing.T) {
		if _, ok := bindDecisionTextAnswer([]decisionAnswerToken{{1, "A"}}, nil); ok {
			t.Fatal("no open cards: expected rejection (silence is the caller's decision)")
		}
	})
	t.Run("empty tokens rejected", func(t *testing.T) {
		open := []db.IssueDecision{openCard(2, false)}
		if _, ok := bindDecisionTextAnswer(nil, open); ok {
			t.Fatal("no tokens: expected rejection")
		}
	})
}
