package handler

import (
	"strings"
	"unicode"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Text decision answers (RUYI-471): a human member's comment whose ENTIRE
// content is a compact token sequence ("1A 2B") answers the issue's open
// decision cards. This is a safety boundary, so it fails closed on purpose:
// only exact token sequences parse, only full coverage of every open card
// binds, and anything else leaves all cards untouched. Agent, system, and
// platform-authored comments never reach this code — it is only invoked from
// the member path of POST /api/issues/{id}/comments.

// decisionAnswerToken is one "1A" / "2BC" element of a compact text answer:
// the 1-based decision number (open cards ordered by created_at) and the
// picked option letters (A = first option).
type decisionAnswerToken struct {
	Seq     int
	Letters string // normalized uppercase, subset of A-D
}

// decisionTokenMaxSeqDigits bounds the digit run so "1" followed by a long
// digit spam cannot overflow int; no issue ever has a million open cards.
const decisionTokenMaxSeqDigits = 6// parseDecisionAnswerTokens parses the whole trimmed content as a compact
// decision answer. It reports ok=false unless the ENTIRE content is a valid
// token sequence: each token is non-zero-leading digits immediately followed
// by one or more option letters A-D (case folded to uppercase), and between
// tokens only whitespace or one of 、，,/ is allowed. Any other character —
// prose, quotes, mention links, code fences, emoji — rejects the whole line.
func parseDecisionAnswerTokens(content string) ([]decisionAnswerToken, bool) {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) == 0 {
		return nil, false
	}
	var tokens []decisionAnswerToken
	i, n := 0, len(runes)
	for {
		dStart := i
		for i < n && runes[i] >= '0' && runes[i] <= '9' {
			i++
		}
		if i == dStart || i-dStart > decisionTokenMaxSeqDigits {
			return nil, false
		}
		if runes[dStart] == '0' {
			return nil, false
		}
		seq := 0
		for _, r := range runes[dStart:i] {
			seq = seq*10 + int(r-'0')
		}
		lStart := i
		for i < n && isDecisionOptionLetter(runes[i]) {
			i++
		}
		if i == lStart {
			return nil, false
		}
		letters := make([]rune, 0, i-lStart)
		for _, r := range runes[lStart:i] {
			letters = append(letters, unicode.ToUpper(r))
		}
		tokens = append(tokens, decisionAnswerToken{Seq: seq, Letters: string(letters)})
		if i == n {
			return tokens, true
		}
		if isDecisionTokenSeparator(runes[i]) {
			for i < n && isDecisionTokenSeparator(runes[i]) {
				i++
			}
			if i == n {
				return nil, false
			}
			continue
		}
		return nil, false
	}
}

func isDecisionOptionLetter(r rune) bool {
	return (r >= 'a' && r <= 'd') || (r >= 'A' && r <= 'D')
}

func isDecisionTokenSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '、', '，', ',', '/':
		return true
	default:
		return false
	}
}

// bindDecisionTextAnswer binds parsed tokens to the issue's open cards using
// the created_at ASC numbering of ListIssueDecisionsForIssue. Fail-closed:
// the tokens must cover exactly 1..N with no gaps, duplicates, or extras,
// and every card's picks must pass validateDecisionAnswer — one violation
// rejects the whole line. An empty open set never binds; the caller decides
// that "no open cards" means silence rather than refusal.
func bindDecisionTextAnswer(tokens []decisionAnswerToken, open []db.IssueDecision) ([]batchDecisionAnswer, bool) {
	if len(tokens) == 0 || len(open) == 0 || len(tokens) != len(open) {
		return nil, false
	}
	bySeq := make(map[int]decisionAnswerToken, len(tokens))
	for _, tk := range tokens {
		if tk.Seq < 1 || tk.Seq > len(open) {
			return nil, false
		}
		if _, dup := bySeq[tk.Seq]; dup {
			return nil, false
		}
		bySeq[tk.Seq] = tk
	}
	bound := make([]batchDecisionAnswer, 0, len(open))
	for i, card := range open {
		tk, ok := bySeq[i+1]
		if !ok {
			return nil, false
		}
		indices := make([]int, 0, len(tk.Letters))
		for _, r := range tk.Letters {
			indices = append(indices, int(r-'A'))
		}
		if err := validateDecisionAnswer(len(parseDecisionOptions(card.Options)), card.MultiSelect, indices); err != nil {
			return nil, false
		}
		item, err := newBatchDecisionAnswer(card, indices)
		if err != nil {
			return nil, false
		}
		bound = append(bound, item)
	}
	return bound, true
}
