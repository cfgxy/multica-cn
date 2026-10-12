package outerloop

import (
	"regexp"
	"strings"
)

// Intent is what a member's comment on a waiting_human plan asks the loop
// to do.
type Intent int

const (
	// IntentContinue resumes the loop: the member replied while the plan was
	// waiting, and nothing in the reply asks it to stop. Review-confirmed
	// default — a member actively commenting on a waiting plan is the
	// highest-probability continue signal, and defaulting to stop would
	// silently strand the task after the member's very first sentence.
	IntentContinue Intent = iota
	// IntentStop ends the plan. When both classes match, stop wins: a human
	// expressing both directions at once gets the conservative reading
	// (停止优先), and the loop can always be re-opened.
	IntentStop
)

// stopIntentTokens are the explicit stop directives a waiting plan honors.
// They are verbs/directives, not nouns, to keep a member's technical prose
// ("停止按钮的样式…") from stopping a plan it was only describing.
var stopIntentTokens = []string{
	"停止", "中止", "终止", "不用了", "不用继续", "到此为止", "别跑了", "别继续",
	"收工", "打住", "取消",
}

// zhStopNegationTokens guard the flip cases — "不要停止" / "别取消" — where
// a stop token appears but the member is refusing the stop. A stop hit is
// discarded when one of these sits in the four runes before it.
var zhStopNegationTokens = []string{"不要", "不能", "不可", "无需", "不用", "别", "莫", "勿", "未", "没", "无"}

var enStopWordRe = regexp.MustCompile(`(?i)\b(stop|abort|cancel|halt|terminate|discontinue)\b`)

// enNegatedStopRe recognizes the English flip shape ("do not stop") so its
// stop word cannot fire; the Chinese negation window above is rune-based
// and cannot see across the space.
var enNegatedStopRe = regexp.MustCompile(`(?i)\b(do not|don't|dont|cannot|can't|cant|never|not)\s+(stop|abort|cancel|halt|terminate|discontinue)\b`)

// ClassifyReply reads a member's comment posted on a waiting_human plan and
// returns the loop action it asks for. Stop detection scans the whole
// content with a negation guard; a negated stop token keeps scanning for a
// later clean one. Anything unmatched is IntentContinue (review-confirmed
// default); when both classes match, stop wins.
func ClassifyReply(content string) Intent {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return IntentContinue
	}
	// "do not stop" / "don't cancel": the member is refusing a stop. Skip
	// the English word scan entirely; the Chinese scan below is unaffected.
	enSkipWords := enNegatedStopRe.MatchString(trimmed)
	if !enSkipWords && enStopWordRe.MatchString(trimmed) {
		return IntentStop
	}
	for _, tok := range stopIntentTokens {
		from := 0
		for {
			at := strings.Index(trimmed[from:], tok)
			if at < 0 {
				break
			}
			at += from
			if !negatedStopAt(trimmed, at) {
				return IntentStop
			}
			from = at + len(tok)
		}
	}
	return IntentContinue
}

// negatedStopAt reports whether the stop token at byte offset at is refused
// by a negation in the four runes immediately before it.
func negatedStopAt(s string, at int) bool {
	if at == 0 {
		return false
	}
	prefixRunes := []rune(s[:at])
	window := 4
	if len(prefixRunes) < window {
		window = len(prefixRunes)
	}
	prefix := string(prefixRunes[len(prefixRunes)-window:])
	for _, tok := range zhStopNegationTokens {
		if strings.HasSuffix(prefix, tok) {
			return true
		}
	}
	return false
}
