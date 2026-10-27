//go:build llmintegration

package promptperplexity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/llm"
	"github.com/multica-ai/multica/server/pkg/promptperplexity"
)

// TestScoreRepeatability measures what D3's acceptance criterion asks for: how
// much a band and an interval move when the SAME assembled prompt is scored
// repeatedly at the same model and temperature. The dashboard declares a
// fluctuation range; this is the run that produces the number behind it.
//
// It is behind a build tag and an explicit env gate because it spends real
// model quota, one call per repetition per profile:
//
//	(cd server && MULTICA_RUN_REAL_LLM_SCORING=1 \
//	  MULTICA_LLM_BASE_URL=... MULTICA_LLM_API_KEY=... \
//	  MULTICA_LLM_DEFAULT_MODEL=gpt-6-luna \
//	  MULTICA_D3_TIERS_FILE=./d3-tiers.json \
//	  go test -tags=llmintegration ./pkg/promptperplexity -run TestScoreRepeatability -count=1 -v)
//
// MULTICA_D3_TIERS_FILE points at a JSON object with the tier bodies
// ({"agent":"...","workspace":"...","squad":"...","project":"..."}) so the
// package keeps no database dependency and the operator chooses which real
// prompt is measured. MULTICA_D3_REPEAT sets the repetition count (default 5).
func TestScoreRepeatability(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_LLM_SCORING") != "1" {
		t.Skip("set MULTICA_RUN_REAL_LLM_SCORING=1 to run a real, quota-consuming D3 scoring test")
	}

	tiersPath := os.Getenv("MULTICA_D3_TIERS_FILE")
	if tiersPath == "" {
		t.Fatal("MULTICA_D3_TIERS_FILE must point at a JSON file holding the tier bodies")
	}
	raw, err := os.ReadFile(tiersPath)
	if err != nil {
		t.Fatal(err)
	}
	var tiers promptperplexity.Tiers
	if err := json.Unmarshal(raw, &tiers); err != nil {
		t.Fatalf("decode %s: %v", tiersPath, err)
	}

	repeat := 5
	if v := os.Getenv("MULTICA_D3_REPEAT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 2 {
			t.Fatalf("MULTICA_D3_REPEAT must be an integer >= 2, got %q", v)
		}
		repeat = n
	}

	client := llm.New(llm.Config{
		APIKey:       os.Getenv("MULTICA_LLM_API_KEY"),
		BaseURL:      os.Getenv("MULTICA_LLM_BASE_URL"),
		DefaultModel: os.Getenv("MULTICA_LLM_DEFAULT_MODEL"),
	})
	if !client.Enabled() {
		t.Fatal("MULTICA_LLM_API_KEY or MULTICA_LLM_BASE_URL must be set")
	}

	for _, profile := range promptperplexity.Profiles {
		t.Run(string(profile), func(t *testing.T) {
			scores := make([]promptperplexity.Score, 0, repeat)
			for i := 0; i < repeat; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				scored, err := promptperplexity.ScoreTiers(ctx, client, profile, tiers)
				cancel()
				if err != nil {
					t.Fatalf("run %d: %v", i+1, err)
				}
				scores = append(scores, scored.Score)
				t.Logf("run %d: band=%s interval=[%.1f, %.1f] weighted=%.4f",
					i+1, scored.Score.Band, scored.Score.PercentLow, scored.Score.PercentHigh, weighted(scored.Score))
			}

			t.Log(summarize(scores))

			// The only assertion is the one a dashboard reader depends on: a
			// repeated score must stay inside one band. A run that flips bands
			// means the declared fluctuation range is not the truth, and the
			// measurement — not the assertion — is what has to change.
			first := scores[0].Band
			for i, s := range scores[1:] {
				if s.Band != first {
					t.Errorf("band is not stable across repeats: run 1 = %s, run %d = %s", first, i+2, s.Band)
				}
			}
		})
	}
}

// weighted is the single number the card shows, computed the way the UI does:
// each sub-dimension's score times its declared weight.
func weighted(s promptperplexity.Score) float64 {
	byKey := make(map[string]float64, len(s.Items))
	for _, item := range s.Items {
		byKey[item.Key] = item.Score
	}
	total := 0.0
	for _, sd := range promptperplexity.SubDimensions {
		total += byKey[sd.Key] * sd.Weight
	}
	return total
}

func summarize(scores []promptperplexity.Score) string {
	lowMin, lowMax := math.Inf(1), math.Inf(-1)
	highMin, highMax := math.Inf(1), math.Inf(-1)
	wMin, wMax, wSum := math.Inf(1), math.Inf(-1), 0.0
	for _, s := range scores {
		lowMin, lowMax = math.Min(lowMin, s.PercentLow), math.Max(lowMax, s.PercentLow)
		highMin, highMax = math.Min(highMin, s.PercentHigh), math.Max(highMax, s.PercentHigh)
		w := weighted(s)
		wMin, wMax, wSum = math.Min(wMin, w), math.Max(wMax, w), wSum+w
	}
	return fmt.Sprintf(
		"repeatability over %d runs: percent_low spread %.1f (%.1f..%.1f), percent_high spread %.1f (%.1f..%.1f), weighted mean %.4f spread %.4f (%.4f..%.4f)",
		len(scores),
		lowMax-lowMin, lowMin, lowMax,
		highMax-highMin, highMin, highMax,
		wSum/float64(len(scores)), wMax-wMin, wMin, wMax,
	)
}
