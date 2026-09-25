package promptquality

import (
	"strings"
	"testing"
)

func TestEstimateTokensIsZeroOnlyForEmptyContent(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
	if got := EstimateTokens(" "); got < 1 {
		t.Errorf("EstimateTokens(\" \") = %d, want at least 1: non-empty content is never free", got)
	}
}

// The prompts this measures are mostly Chinese. An English-calibrated
// chars/4 rule reports roughly a third of the real cost for them, which would
// make a Chinese-heavy prompt version look cheaper than an English one of the
// same weight.
func TestEstimateTokensCountsCJKDenserThanASCII(t *testing.T) {
	cjk := EstimateTokens(strings.Repeat("禁", 100))
	ascii := EstimateTokens(strings.Repeat("a", 100))
	if cjk <= ascii {
		t.Errorf("cjk = %d, ascii = %d: 100 CJK characters must not estimate at or below 100 ASCII characters", cjk, ascii)
	}
	// Roughly one token per CJK character, well above the ~0.25 of ASCII.
	if cjk < 80 || cjk > 120 {
		t.Errorf("cjk = %d, want near 100", cjk)
	}
	if ascii < 20 || ascii > 40 {
		t.Errorf("ascii = %d, want near 25", ascii)
	}
}

func TestEstimateTokensGrowsMonotonically(t *testing.T) {
	short := EstimateTokens("the discipline rules")
	long := EstimateTokens("the discipline rules" + strings.Repeat(" and more text", 20))
	if long <= short {
		t.Errorf("long = %d, short = %d: appending content must not lower the estimate", long, short)
	}
}

// Counting by byte would charge a 3-byte CJK character three times.
func TestEstimateTokensCountsRunesNotBytes(t *testing.T) {
	if got, want := EstimateTokens("禁"), int64(3); got >= want {
		t.Errorf("EstimateTokens(\"禁\") = %d, want below %d (its byte length)", got, want)
	}
}
