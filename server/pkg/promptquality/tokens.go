package promptquality

import "unicode"

// EstimateTokens approximates how many tokens a prompt's stored content costs
// when injected (D1, static half).
//
// It is an estimate and the dashboard labels it as one. The exact number
// depends on the provider's tokenizer, which differs per model and is not
// available server-side; running a real tokenizer per version per day would
// also cost more than the figure is worth. What the dashboard needs from this
// number is comparability between versions of the same prompt, and a rule that
// is stable and monotonic gives that.
//
// The rule is split by script because our prompts are predominantly Chinese.
// Byte-pair tokenizers spend roughly one token per CJK character but roughly
// one per four ASCII characters; a single chars/4 rule — the usual English
// shorthand — would report a Chinese paragraph at about a third of its real
// cost, and a version that traded English text for Chinese would appear to
// have gotten cheaper while actually getting more expensive.
func EstimateTokens(content string) int64 {
	var cjk, other int64
	for _, r := range content {
		if isCJK(r) {
			cjk++
			continue
		}
		other++
	}
	if cjk == 0 && other == 0 {
		return 0
	}
	// Integer division rounds the ASCII side down; the +3 keeps any non-empty
	// content from estimating as free.
	return cjk + (other+3)/4
}

// isCJK covers the ranges our prompts actually use: Han characters plus the
// CJK punctuation our Chinese copy rules mandate (，。「」 and friends), which
// tokenizers charge at the same density.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303F) || // CJK symbols and punctuation
		(r >= 0xFF00 && r <= 0xFFEF) //   fullwidth forms
}
