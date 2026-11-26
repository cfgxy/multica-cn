package outerloop

import (
	"regexp"
	"strings"
)

// HasEndMarker reports whether an agent's report carries the stage-complete
// end marker under the plan's closing contract.
//
// The contract is deliberately narrow (整行声明式判定): a marker only counts
// when, after stripping markdown wrapping, a line IS the marker or ENDS with
// it. A marker buried mid-line ("阶段完成：STAGE_COMPLETE，下一步见下") does
// not count — the workspace convention puts the marker at the end of the
// closing line, and anything looser would fire on prose that merely mentions
// the token.
//
// Negated occurrences do not count: a line whose pre-marker text carries a
// negation ("未写【STAGE_COMPLETE】", "I did not write STAGE_COMPLETE")
// describes the marker's absence. Every occurrence being negated means no
// marker. Ticket status is intentionally NOT an input — the predicate sees
// only the report text (验收 1).
//
// Known accepted bias: a marker quoted inside a code fence (a usage example)
// counts as present. The wrong direction here is an early stop, which a
// human or the next round can undo at bounded cost; the opposite bias
// (missing a real marker) would keep re-dispatching finished work forever.
// Table tests lock this behavior in.
func HasEndMarker(content string) bool {
	if content == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		if lineHasMarker(line) {
			return true
		}
	}
	return false
}

func lineHasMarker(line string) bool {
	s := stripMarkdownWrapping(line)
	if s == StageCompleteMarker {
		return true
	}
	if !strings.HasSuffix(s, StageCompleteMarker) {
		return false
	}
	before := s[:len(s)-len(StageCompleteMarker)]
	if before == "" {
		return true
	}
	return !negatedPrefix(before)
}

// stripMarkdownWrapping peels the common markdown shells off one line —
// blockquote/list/heading prefixes, bold/italic/backtick/fence accents,
// paired brackets and trailing punctuation — so the declaration shape
// survives the formatting a report naturally carries.
func stripMarkdownWrapping(line string) string {
	s := strings.TrimSpace(line)
	for i := 0; i < 12; i++ { // wrapping depth is tiny; bound defends against pathological nesting
		next := stripLeadingMarkdown(stripTrailingMarkdown(s))
		if next == s {
			return s
		}
		s = next
	}
	return s
}

var leadingMarkdownTokens = []string{
	">>", ">", "###", "##", "#",
	"**", "__", "*", "+", "-", "~", "`",
	"【", "「", "『", "〔", "（", "(", "[", "{",
	"“", "‘", "\"", "'", "《", "〈",
	"—", "－－", "–", "－", "·",
}

var trailingMarkdownRunes = func() string {
	// Whitespace, paired brackets, quotes, accents and CJK/ASCII punctuation
	// that may sit between the declaration and the physical end of the line.
	set := " \t\r\n\v\f*_'`~^\"“”‘’" +
		"()[]{}<>（）【】《》〈〉「」『』〔〕" +
		"。，、；：！？.,:;!？…·—－–-＝=｜|"
	return set
}()

var numberedPrefixRe = regexp.MustCompile(`^\d{1,3}[.)、．]\s*`)

func stripLeadingMarkdown(s string) string {
	for {
		s = strings.TrimLeft(s, " \t")
		if len(s) > 1 {
			if loc := numberedPrefixRe.FindStringIndex(s); loc != nil {
				s = s[loc[1]:]
				continue
			}
		}
		matched := false
		for _, p := range leadingMarkdownTokens {
			if strings.HasPrefix(s, p) && len(s) > len(p) {
				s = s[len(p):]
				matched = true
				break
			}
		}
		if !matched {
			return s
		}
	}
}

func stripTrailingMarkdown(s string) string {
	return strings.TrimRight(s, trailingMarkdownRunes)
}

// negatedPrefix reports whether the text immediately before a marker hit
// negates it ("未写"、"没有"、"no …"). It is a heuristic by design: the two
// failure directions are a missed marker (loop runs one bounded extra round)
// and a negation miss (early stop), and both are cheaper than a full NLP
// pass. Table tests pin every shape this function is trusted for.
func negatedPrefix(before string) bool {
	b := strings.TrimRight(before, trailingMarkdownRunes)
	if b == "" {
		return false
	}
	if englishNegationInTail(b) {
		return true
	}
	for _, tok := range zhNegationTokens {
		if strings.HasSuffix(b, tok) {
			return true
		}
	}
	runes := []rune(b)
	window := 4
	if len(runes) < window {
		window = len(runes)
	}
	tail := runes[len(runes)-window:]
	for _, r := range []rune("未没无缺") {
		if containsRune(tail, r) {
			return true
		}
	}
	return false
}

// zhNegationTokens are the multi-character Chinese negations that must match
// as suffixes of the pre-marker text; the single-character negatives are
// handled by the short window above so "我没有写" still negates while a
// distant 无 ("…已收敛，无阻塞：STAGE_COMPLETE") is far enough away to not
// fire. The window trade-off is table-tested.
var zhNegationTokens = []string{
	"未写", "没写", "未加", "没加", "未包含", "没包含",
	"不含", "不包括", "缺少", "缺失", "尚未", "尚无",
	"漏写", "漏掉", "省略", "遗漏", "忘写", "没有",
}

var englishNegationWords = map[string]bool{
	"no": true, "not": true, "none": true, "never": true, "without": true,
	"missing": true, "miss": true, "lacks": true, "lack": true, "lacking": true,
	"lacked": true, "omit": true, "omitted": true, "omitting": true,
	"omits": true, "skipped": true, "skip": true, "skipping": true,
	"absent": true,
}

var englishWordRe = regexp.MustCompile(`[A-Za-z0-9']+`)

// englishNegationInTail checks the trailing word tokens (up to three) so
// "I did not write STAGE_COMPLETE" negates even though "write" is the last
// word, while "notarized" never matches "not".
func englishNegationInTail(s string) bool {
	words := englishWordRe.FindAllString(s, -1)
	start := 0
	if len(words) > 3 {
		start = len(words) - 3
	}
	for _, w := range words[start:] {
		if englishNegationWords[strings.ToLower(w)] {
			return true
		}
	}
	return false
}

func containsRune(rs []rune, r rune) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}
