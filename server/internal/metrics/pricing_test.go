package metrics

import "testing"

func TestPriceForModelAliasAnthropicCurrentGeneration(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "claude-sonnet-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-sonnet-5", InputPerM: 2, CacheReadPerM: 0.2, CacheWritePerM: 2.5, OutputPerM: 10},
		},
		{
			model: "anthropic:claude-sonnet-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-sonnet-5", InputPerM: 2, CacheReadPerM: 0.2, CacheWritePerM: 2.5, OutputPerM: 10},
		},
		{
			model: "claude-5-sonnet",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-sonnet-5", InputPerM: 2, CacheReadPerM: 0.2, CacheWritePerM: 2.5, OutputPerM: 10},
		},
		{
			model: "claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "anthropic/claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "claude-opus-4-8",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-4.8", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
		// Opus 5 sits on the same 5/25 Opus tier as 4.5-4.8.
		{
			model: "claude-opus-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-5", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
		{
			model: "anthropic/claude-opus-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-5", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
		// Claude Code reports the 1M-context beta with a bracketed suffix.
		{
			model: "claude-opus-5[1m]",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-5", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}
}

func TestPriceForModelAliasCodexGPT56(t *testing.T) {
	// Official rates from OpenAI's GPT-5.6 announcement: cache read = 0.1x
	// input (90% cached-input discount), cache write = 1.25x input.
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "gpt-5.6-sol",
			want:  ModelPrice{Provider: "openai", Model: "gpt-5.6-sol", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 30},
		},
		{
			model: "openai:gpt-5.6-terra",
			want:  ModelPrice{Provider: "openai", Model: "gpt-5.6-terra", InputPerM: 2.5, CacheReadPerM: 0.25, CacheWritePerM: 3.125, OutputPerM: 15},
		},
		{
			model: "openai/gpt-5.6-luna",
			want:  ModelPrice{Provider: "openai", Model: "gpt-5.6-luna", InputPerM: 1, CacheReadPerM: 0.1, CacheWritePerM: 1.25, OutputPerM: 6},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}

	// Unknown suffixed variants must NOT borrow a 5.6 tier — the alias is an
	// anchored exact match, mirroring the frontend's exact-match resolver.
	// The dash-normalized ids (`gpt-5-6-luna`) must also miss: the real Codex
	// slug is always dotted and the frontend does not dash-normalize, so both
	// sides surface these as unmapped instead of silently pricing them.
	for _, model := range []string{
		"gpt-5.6-luna-pro",
		"gpt-5.6-luna/unknown",
		"gpt-5.6-sol-high",
		"gpt-5.6-mini",
		"gpt-5-6-luna",
		"gpt-5-6-sol",
		"gpt-5-6-terra",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Fatalf("PriceForModelAlias(%q) unexpectedly resolved to %+v; want unmapped", model, got)
		}
	}
}

// TestPriceForModelAliasGrok pins the xAI catalog to the published rates
// (docs.x.ai/developers/pricing). Before these rows existed every Grok token
// took the unpriced branch in RecordLLMUsage, so llm_cost_usd reported zero
// Grok spend while the tokens piled up in llm_unpriced_tokens.
func TestPriceForModelAliasGrok(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "grok-4.6",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.6", InputPerM: 2, CacheReadPerM: 0.5, CacheWritePerM: 2, OutputPerM: 6},
		},
		{
			model: "xai:grok-4.6",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.6", InputPerM: 2, CacheReadPerM: 0.5, CacheWritePerM: 2, OutputPerM: 6},
		},
		{
			model: "grok-4.5",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.5", InputPerM: 2, CacheReadPerM: 0.3, CacheWritePerM: 2, OutputPerM: 6},
		},
		{
			model: "xai:grok-4.5",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.5", InputPerM: 2, CacheReadPerM: 0.3, CacheWritePerM: 2, OutputPerM: 6},
		},
		{
			model: "xai/grok-4.5",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.5", InputPerM: 2, CacheReadPerM: 0.3, CacheWritePerM: 2, OutputPerM: 6},
		},
		{
			model: "grok-4.3",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.3", InputPerM: 1.25, CacheReadPerM: 0.2, CacheWritePerM: 1.25, OutputPerM: 2.5},
		},
		{
			model: "grok-build-0.1",
			want:  ModelPrice{Provider: "xai", Model: "grok-build-0.1", InputPerM: 1, CacheReadPerM: 0.2, CacheWritePerM: 1, OutputPerM: 2},
		},
		{
			model: "grok-4.20-multi-agent-0309",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.20-multi-agent-0309", InputPerM: 1.25, CacheReadPerM: 0.2, CacheWritePerM: 1.25, OutputPerM: 2.5},
		},
		{
			model: "grok-4.20-0309-reasoning",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.20-0309-reasoning", InputPerM: 1.25, CacheReadPerM: 0.2, CacheWritePerM: 1.25, OutputPerM: 2.5},
		},
		{
			model: "grok-4.20-0309-non-reasoning",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.20-0309-non-reasoning", InputPerM: 1.25, CacheReadPerM: 0.2, CacheWritePerM: 1.25, OutputPerM: 2.5},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}

	// `grok-composer-*` ships in the Grok Build catalog but xAI publishes no
	// rate for it, so it must stay unmapped rather than inherit grok-4.5's.
	// Suffixed and dash-spelled variants must miss for the same reason the
	// gpt-5.6 rows do: the frontend resolver is an exact match that does not
	// dash-normalize non-Anthropic ids, so both sides agree on "unmapped".
	for _, model := range []string{
		"grok-composer-2.5-fast",
		"grok-composer-2.5",
		"grok-4.6-fast",
		"grok-4-6",
		"grok-4.5-fast",
		"grok-4-5",
		"grok-4.20-0309",
		"grok",
		"unknown",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Fatalf("PriceForModelAlias(%q) unexpectedly resolved to %+v; want unmapped", model, got)
		}
	}
}

// TestGrokPricingMatchesRecordedTurn re-derives the cost of a real
// grok 0.2.106 turn from the table and checks it against the costUsdTicks xAI
// returned for that same turn (1 tick = 1e-10 USD). This is the end-to-end
// proof that both the rates and the cached-input bucketing are right.
func TestGrokPricingMatchesRecordedTurn(t *testing.T) {
	// Captured payload: inputTokens 12929, cachedReadTokens 10880,
	// outputTokens 29, totalTokens 12958, costUsdTicks 75360000. Grok counts
	// the cached prefix inside inputTokens, so the uncached remainder is
	// 12929 - 10880 = 2049 (see excludeACPCachedInput in pkg/agent/hermes.go).
	const (
		uncachedInput = int64(2049)
		cacheRead     = int64(10880)
		output        = int64(29)
		wantUSD       = 75360000 / 1e10
	)

	price, ok := PriceForModelAlias("grok-4.5")
	if !ok {
		t.Fatal("grok-4.5 did not resolve")
	}
	got := tokenCostUSD(uncachedInput, price.InputPerM) +
		tokenCostUSD(cacheRead, price.CacheReadPerM) +
		tokenCostUSD(output, price.OutputPerM)

	if diff := got - wantUSD; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("recomputed cost = %.10f, want %.10f (xAI costUsdTicks)", got, wantUSD)
	}
}

// TestPriceForModelAliasAlibabaMoonshotVolcengine pins the pay-as-you-go
// rates for the Chinese-model runtimes (Qwen / Kimi) added from models.dev,
// and the transport spellings that reach them: `provider:model` (Hermes
// custom providers), `provider/model` (opencode), and bare ids. Volcengine's
// `ark-code-latest` rolling alias is covered as unmapped in
// TestPriceForModelAliasNoFalseBorrowing.
func TestPriceForModelAliasAlibabaMoonshotVolcengine(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "qwen3.7-plus",
			want:  ModelPrice{Provider: "alibaba", Model: "qwen3.7-plus", InputPerM: 0.40, CacheReadPerM: 0.04, CacheWritePerM: 0.50, OutputPerM: 1.60},
		},
		{
			model: "alibaba-coding-plan:qwen3.7-plus",
			want:  ModelPrice{Provider: "alibaba", Model: "qwen3.7-plus", InputPerM: 0.40, CacheReadPerM: 0.04, CacheWritePerM: 0.50, OutputPerM: 1.60},
		},
		{
			model: "qwen3.6-flash",
			want:  ModelPrice{Provider: "alibaba", Model: "qwen3.6-flash", InputPerM: 0.25, CacheReadPerM: 0.025, CacheWritePerM: 0.3125, OutputPerM: 1.50},
		},
		{
			model: "alibaba-coding-plan:qwen3.8-max",
			want:  ModelPrice{Provider: "alibaba", Model: "qwen3.8-max", InputPerM: 2.00, CacheReadPerM: 0.17, CacheWritePerM: 2.50, OutputPerM: 6.00},
		},
		{
			model: "custom:qwen3.8-max-preview[1m]",
			want:  ModelPrice{Provider: "alibaba", Model: "qwen3.8-max-preview", InputPerM: 0, CacheReadPerM: 0, CacheWritePerM: 0, OutputPerM: 0},
		},
		{
			model: "kimi-coding:kimi-k3",
			want:  ModelPrice{Provider: "moonshotai", Model: "kimi-k3", InputPerM: 3.0, CacheReadPerM: 0.30, CacheWritePerM: 3.0, OutputPerM: 15.0},
		},
		{
			// Kimi Code CLI reports `kimi-code/k3`.
			model: "kimi-code/k3",
			want:  ModelPrice{Provider: "moonshotai", Model: "kimi-k3", InputPerM: 3.0, CacheReadPerM: 0.30, CacheWritePerM: 3.0, OutputPerM: 15.0},
		},
		{
			// `custom:anthropic/claude-opus-4.7` (provider prefix + nested
			// slash path) must still resolve to the anthropic Opus tier via
			// substring matching, mirroring the frontend stripProvider
			// regression case.
			model: "custom:anthropic/claude-opus-4.7",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-4.7", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 6.25, OutputPerM: 25.00},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}
}

// TestPriceForModelAliasNoFalseBorrowing guards the anchored rules: a preview
// SKU must not inherit the GA tier, a distinct CodeBuddy SKU must not inherit
// Kimi K3, unknown suffixed variants must stay unmapped, empty bracket tags
// (`qwen3.7-plus[]` etc.) must stay unmapped to match the frontend's
// `\[[^\]]+\]$` tag stripping, and the Volcengine `ark-code-latest` rolling
// alias must stay unmapped.
func TestPriceForModelAliasNoFalseBorrowing(t *testing.T) {
	for _, model := range []string{
		"qwen3.8-max-preview",
		"qwen3.8-max-preview[1m]",
		"kimi-k3-1",
		"qwen3.8-max-extra",
		"qwen3.8-max[",
		"qwen3.8-max[1m]-extra",
		"qwen3.8-max-preview[1m]-extra",
	} {
		got, ok := PriceForModelAlias(model)
		if !ok {
			continue
		}
		if got.Model == "qwen3.8-max" || got.Model == "kimi-k3" {
			t.Fatalf("PriceForModelAlias(%q) borrowed %s; want the SKU's own tier or unmapped", model, got.Model)
		}
	}

	// A distinct SKU that borrows nothing must resolve to its own row.
	for _, tc := range []struct {
		model     string
		wantModel string
	}{
		{"qwen3.8-max-preview[1m]", "qwen3.8-max-preview"},
		{"qwen3.8-max-preview[context]", "qwen3.8-max-preview"},
		{"qwen3.8-max[1m]", "qwen3.8-max"},
	} {
		got, ok := PriceForModelAlias(tc.model)
		if !ok || got.Model != tc.wantModel {
			t.Fatalf("PriceForModelAlias(%q) = %+v (ok=%v); want %s", tc.model, got, ok, tc.wantModel)
		}
	}

	for _, model := range []string{
		"qwen3.8-max-extra",
		"kimi-k3-1",
		"qwen3.7-plus-extra",
		"qwen3.6-flash-extra",
		"qwen3.8-max-preview-extra",
		"custom:ark-code-latest",
		// Empty bracket tags: the frontend's `\[[^\]]+\]$` tag stripper
		// leaves these unmapped, so the backend must too.
		"qwen3.7-plus[]",
		"qwen3.6-flash[]",
		"qwen3.8-max[]",
		"qwen3.8-max-preview[]",
		// Cursor's generic ids must not resolve bare — `auto` is also what
		// codex reports, so resolving it here would be a cross-provider
		// misprice rather than a coverage win (RUYI-664).
		"auto",
		"composer-1",
		"composer-2.5",
		"codex/auto",
		"openai/auto",
	} {
		if _, ok := PriceForModelAlias(model); ok {
			t.Fatalf("PriceForModelAlias(%q) unexpectedly resolved", model)
		}
	}
}

// TestPriceForModelAliasContextTagStripping pins the `[1m]` context-variant
// suffix normalization across every rule, including the anchored Codex / Grok /
// Kimi rules that do not carry a per-rule optional bracket group. Claude Code
// (and other harnesses) append a context-window tag such as `[1m]` to the
// model id; it is the same SKU at the same tier, so the row must price instead
// of falling into the unpriced bucket in RecordLLMUsage. Mirrors the frontend's
// `stripContextTag` (`\[[^\]]+\]$`) in packages/views/runtimes/utils.ts.
func TestPriceForModelAliasContextTagStripping(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "grok-4.5[1m]",
			want:  ModelPrice{Provider: "xai", Model: "grok-4.5", InputPerM: 2.00, CacheReadPerM: 0.30, CacheWritePerM: 2.00, OutputPerM: 6.00},
		},
		{
			model: "gpt-5.6-luna[1m]",
			want:  ModelPrice{Provider: "openai", Model: "gpt-5.6-luna", InputPerM: 1.00, CacheReadPerM: 0.10, CacheWritePerM: 1.25, OutputPerM: 6.00},
		},
		{
			model: "kimi-k3[1m]",
			want:  ModelPrice{Provider: "moonshotai", Model: "kimi-k3", InputPerM: 3.0, CacheReadPerM: 0.30, CacheWritePerM: 3.0, OutputPerM: 15.0},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}

	// The tag stripper is anchored at end-of-string with a non-empty tag, so it
	// must not turn these misses into hits: a trailing bracket that is not a
	// complete end-of-string tag, and an empty tag, both stay unmapped — the
	// same guard the frontend keeps.
	for _, model := range []string{
		"grok-4.5[1m]-extra",
		"gpt-5.6-luna[]",
		"kimi-k3[",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Fatalf("PriceForModelAlias(%q) unexpectedly resolved to %+v; want unmapped", model, got)
		}
	}
}

func TestPriceForModelAliasAnthropicFable51(t *testing.T) {
	// Fable 5.1 is its own SKU on the same Mythos-class tier as Fable 5, but
	// with cache reads at 0.025x input ($0.25) instead of the usual 0.1x. A
	// Fable 5 alias that did not stop at the version would swallow the `-1`
	// suffix and bill those reads at 4x, so every spelling below must land on
	// the Fable 5.1 row specifically.
	fable51 := ModelPrice{Provider: "anthropic", Model: "claude-fable-5-1", InputPerM: 10, CacheReadPerM: 0.25, CacheWritePerM: 12.5, OutputPerM: 50}
	fable5 := ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50}
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{model: "claude-fable-5-1", want: fable51},
		{model: "anthropic/claude-fable-5-1", want: fable51},
		{model: "anthropic:claude-fable-5-1", want: fable51},
		// Copilot reports Claude models dotted.
		{model: "claude-fable-5.1", want: fable51},
		// Claude Code reports the 1M-context variant with a bracketed suffix.
		{model: "claude-fable-5-1[1m]", want: fable51},
		// Fable 5 must keep resolving to its own row, including its 1M form.
		{model: "claude-fable-5", want: fable5},
		{model: "claude-fable-5[1m]", want: fable5},
		// The frontend resolver strips a trailing date snapshot / `-latest`
		// before its exact-key lookup (`stripDate` in
		// packages/views/runtimes/utils.ts), so these forms price there. Both
		// rules have to admit them too, otherwise the dashboard and
		// RecordLLMUsage disagree on the same id.
		{model: "claude-fable-5-20260401", want: fable5},
		{model: "claude-fable-5-2026-04-01", want: fable5},
		{model: "claude-fable-5-latest", want: fable5},
		{model: "claude-fable-5-20260401[1m]", want: fable5},
		{model: "claude-fable-5-1-20260901", want: fable51},
		{model: "claude-fable-5-1-latest", want: fable51},
		{model: "claude-fable-5-1-20260901[1m]", want: fable51},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}

	// A later Fable minor is a distinct SKU at an unknown rate: it must stay
	// unmapped and surface in the unpriced diagnostic rather than borrow a
	// neighbour's tier. This is the same failure the `-1` suffix had against
	// the Fable 5 rule, so guard it on the 5.1 rule as well.
	for _, model := range []string{
		"claude-fable-5-2",
		"claude-fable-5.2",
		"claude-fable-5-10",
		"claude-fable-5.10",
		"claude-fable-5-1x",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Errorf("PriceForModelAlias(%q) resolved to %+v; want unmapped", model, got)
		}
	}

	// An admitted suffix only counts when it ENDS the id. These rules are
	// substring matches, so a terminator whose alternatives are not anchored
	// still fires on anything that merely starts with one — an unknown
	// qualifier would silently borrow the tier of whichever row it prefixed,
	// while the frontend (which anchors both `stripDate` and the bracket tag)
	// leaves it unmapped. Same id, two different costs.
	for _, model := range []string{
		"claude-fable-5-1-latest-preview",
		"claude-fable-5-1-20260901x",
		"claude-fable-5-1-2026-09-01-preview",
		"claude-fable-5-1[1m]junk",
		"claude-fable-5-latest-preview",
		"claude-fable-5-20260401-preview",
		"claude-fable-5[1m]junk",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Errorf("PriceForModelAlias(%q) resolved to %+v; want unmapped", model, got)
		}
	}

	// A doubly-tagged id must not sneak back in through the tag-stripping
	// retry: peeling `[2m]` leaves `[1m]`, which the rule above rejected on
	// the raw form for good reason. The frontend strips one tag and does not
	// re-strip, so pricing these here would put two different costs on one
	// usage row.
	for _, model := range []string{
		"claude-fable-5[1m][2m]",
		"claude-fable-5-1[1m][2m]",
	} {
		if got, ok := PriceForModelAlias(model); ok {
			t.Errorf("PriceForModelAlias(%q) resolved to %+v; want unmapped", model, got)
		}
	}
}

// TestPriceForModelProviderQualified pins the resolver entry point that takes
// the reporter's provider into account. Bare `auto` must stay unmapped — codex
// reports `auto` too, so a bare lookup would bill codex runs at Cursor's rate
// — while `cursor` + `auto` reaches Cursor's own row. This is the exact
// cross-provider misprice RUYI-664 was written to keep closed.
func TestPriceForModelProviderQualified(t *testing.T) {
	cursorAuto := ModelPrice{Provider: "cursor", Model: "auto", InputPerM: 1.25, CacheReadPerM: 0.25, CacheWritePerM: 0, OutputPerM: 6.00}

	if got, ok := PriceForModel("auto", "cursor"); !ok || got != cursorAuto {
		t.Fatalf("PriceForModel(\"auto\", \"cursor\") = %+v (ok=%v); want %+v", got, ok, cursorAuto)
	}

	// The collision this Issue is about: codex reports the SAME literal id.
	// No qualified row exists for codex, and the bare fallback must not
	// borrow Cursor's, so this stays unpriced.
	if got, ok := PriceForModel("auto", "codex"); ok {
		t.Fatalf("PriceForModel(\"auto\", \"codex\") = %+v; want unmapped — that is Cursor's rate on a codex run", got)
	}
	if got, ok := PriceForModel("auto", ""); ok {
		t.Fatalf("PriceForModel(\"auto\", \"\") = %+v; want unmapped without a provider", got)
	}

	// An id that already carries its own provider is not doubled.
	if got, ok := PriceForModel("cursor/auto", "cursor"); !ok || got != cursorAuto {
		t.Fatalf("PriceForModel(\"cursor/auto\", \"cursor\") = %+v (ok=%v); want %+v", got, ok, cursorAuto)
	}

	// Cursor's own SKUs resolve qualified; another provider's prefix does not
	// take the Cursor row via the bare fallback either.
	composer := ModelPrice{Provider: "cursor", Model: "composer-2", InputPerM: 0.50, CacheReadPerM: 0.20, CacheWritePerM: 0, OutputPerM: 2.50}
	if got, ok := PriceForModel("composer-2", "cursor"); !ok || got != composer {
		t.Fatalf("PriceForModel(\"composer-2\", \"cursor\") = %+v (ok=%v); want %+v", got, ok, composer)
	}
	if got, ok := PriceForModel("composer-2", "codex"); ok {
		t.Fatalf("PriceForModel(\"composer-2\", \"codex\") = %+v; want unmapped", got)
	}

	// A non-colliding model still resolves through the bare fallback when the
	// reporter's provider has no qualified row.
	kimi := ModelPrice{Provider: "moonshotai", Model: "kimi-k3", InputPerM: 3.0, CacheReadPerM: 0.30, CacheWritePerM: 3.0, OutputPerM: 15.0}
	if got, ok := PriceForModel("kimi-k3", "codex"); !ok || got != kimi {
		t.Fatalf("PriceForModel(\"kimi-k3\", \"codex\") = %+v (ok=%v); want %+v", got, ok, kimi)
	}
}

// TestZhipuGLM5Priced pins the GLM family the Go table used to miss entirely,
// so every local turn fell into the unpriced bucket and converted to $0.00. A
// conversion that is always zero is worse than no conversion: a cost alert
// sitting on top of it can never fire.
//
// The case also pins the shape that matters most here: cache_read dominates
// real usage, so a row with the wrong cache rate under-prices by more than
// the input row can correct.
func TestZhipuGLM5Priced(t *testing.T) {
	price, ok := PriceForModelAlias("glm-5")
	if !ok {
		t.Fatal("PriceForModelAlias(\"glm-5\") did not resolve; the glm family is missing from the Go table again")
	}
	want := ModelPrice{Provider: "zhipu", Model: "glm-5", InputPerM: 1.00, CacheReadPerM: 0.20, CacheWritePerM: 1.00, OutputPerM: 3.20}
	if price != want {
		t.Fatalf("PriceForModelAlias(\"glm-5\") = %+v, want %+v", price, want)
	}

	// A context tag is the same SKU at the same tier.
	if got, ok := PriceForModelAlias("glm-5[1m]"); !ok || got != want {
		t.Fatalf("PriceForModelAlias(\"glm-5[1m]\") = %+v (ok=%v); want %+v", got, ok, want)
	}
	// So is a provider-prefixed form, mirroring the Anthropic cases.
	if got, ok := PriceForModelAlias("custom:glm-5"); !ok || got != want {
		t.Fatalf("PriceForModelAlias(\"custom:glm-5\") = %+v (ok=%v); want %+v", got, ok, want)
	}
	// The 4.5 generation has its own rows at different rates; a family rule
	// that swallowed them would bill -air at 5x and -flash (free) at 5x too.
	for _, model := range []string{"glm-4.5-air", "glm-4.5-airx", "glm-4.5-flash", "glm-4.5-x"} {
		if got, ok := PriceForModelAlias(model); !ok || got.Model == "glm-5" {
			t.Fatalf("PriceForModelAlias(%q) = %+v (ok=%v); want its own row, not glm-5", model, got, ok)
		}
	}

	// A median-sized real turn must convert to a non-zero cost.
	const (
		input     = 43_068
		output    = 1_198
		cacheRead = 240_128
	)
	got := EstimateUsageCost("glm-5", "zhipu", 0, input, output, cacheRead, 0)
	if got.Source != CostSourceTable {
		t.Fatalf("median turn source = %q; want %q", got.Source, CostSourceTable)
	}
	if got.USD <= 0 {
		t.Fatalf("median turn cost = %v; want non-zero", got.USD)
	}
	// Guard the cache term specifically: it is the largest component, so a
	// row that silently priced cache reads at 0 would still pass a loose
	// "non-zero" check on the input term alone.
	cacheTerm := tokenCostUSD(cacheRead, price.CacheReadPerM)
	if cacheTerm <= 0 {
		t.Fatalf("cache-read term = %v; want non-zero", cacheTerm)
	}
	if cacheTerm <= tokenCostUSD(input, price.InputPerM) {
		t.Fatalf("cache-read term %v should exceed the input term %v at a cache:input ratio above 5:1",
			cacheTerm, tokenCostUSD(input, price.InputPerM))
	}
}

// TestEstimateUsageCostKeepsUnknownApartFromZero pins the four-state
// conversion: provider-reported, table estimate, genuinely free, and unknown.
// The two 0s are different states — folding unpriced rows into aggregates as
// $0 quietly moves medians and never fires a budget alert.
func TestEstimateUsageCostKeepsUnknownApartFromZero(t *testing.T) {
	cases := []struct {
		name  string
		model string
		// provider is the reporter, and it is part of the price lookup: some
		// ids (`auto`) are only meaningful once you know who sent them.
		// Empty means "no provider recorded", which is what the older rows
		// look like and what the bare-model fallback exists for.
		provider string
		ticks    int64
		in, out  int64
		cr, cw   int64

		wantSource CostSource
		wantZero   bool
	}{
		{
			name:     "provider price wins over the table",
			model:    "glm-5",
			provider: "zhipu",
			ticks:    1_234_000_000,
			in:       43_068, out: 1_198, cr: 240_128,
			wantSource: CostSourceProvider,
		},
		{
			name:  "rate table estimate",
			model: "glm-5",
			in:    43_068, out: 1_198, cr: 240_128,
			wantSource: CostSourceTable,
		},
		{
			name:     "unknown model, real tokens: unpriced",
			model:    "unknown",
			provider: "codex",
			in:       28_833, out: 969, cr: 122_880,
			wantSource: CostSourceUnpriced, wantZero: true,
		},
		{
			name:  "free-tier model, real tokens: known free",
			model: "glm-4.5-flash",
			in:    28_833, out: 969, cr: 122_880,
			wantSource: CostSourceZero, wantZero: true,
		},
		{
			name:       "no tokens at all: genuinely free",
			model:      "glm-5",
			wantSource: CostSourceZero, wantZero: true,
		},
	}

	for _, tc := range cases {
		got := EstimateUsageCost(tc.model, tc.provider, tc.ticks, tc.in, tc.out, tc.cr, tc.cw)
		if got.Source != tc.wantSource {
			t.Errorf("%s: source = %q, want %q", tc.name, got.Source, tc.wantSource)
		}
		if tc.wantZero && got.USD != 0 {
			t.Errorf("%s: USD = %v, want 0", tc.name, got.USD)
		}
		if !tc.wantZero && got.USD <= 0 {
			t.Errorf("%s: USD = %v, want > 0", tc.name, got.USD)
		}
	}

	// The two 0s are different: only one of them may enter an aggregate.
	unknown := EstimateUsageCost("unknown", "codex", 0, 28_833, 969, 122_880, 0)
	free := EstimateUsageCost("glm-5", "", 0, 0, 0, 0, 0)
	freeTier := EstimateUsageCost("glm-4.5-flash", "zhipu", 0, 28_833, 969, 122_880, 0)
	if unknown.Priceable() {
		t.Error("unpriced row is Priceable(); it must be excluded from medians and budgets")
	}
	if !free.Priceable() {
		t.Error("genuinely free row is not Priceable(); a real 0 belongs in the median")
	}
	if !freeTier.Priceable() {
		t.Error("known-free-tier row is not Priceable(); a real 0 belongs in the median")
	}
}

// TestEstimateUsageCostPricesARealIssuesRows prices the four usage rows of one
// real issue the way the Issue page prices them.
//
// This is the issue-level half of "one price list, one number" (RUYI-664): a
// whole issue's rows, not hand-picked ones. A mismatch here means the Issue
// page and the CLI disagree about what the issue cost.
//
// Mirrored in packages/views/runtimes/utils.test.ts; both sides pin 0.392574.
func TestEstimateUsageCostPricesARealIssuesRows(t *testing.T) {
	type row struct{ in, out, cacheRead int64 }
	rows := []row{
		{14_487, 696, 108_800},
		{8_753, 501, 122_752},
		{2_645, 303, 102_912},
		{2_173, 134, 72_064},
	}

	wantPerRow := []float64{0.147715, 0.120171, 0.073771, 0.050917}
	const wantTotal = 0.392574

	var total float64
	for i, r := range rows {
		got := EstimateUsageCost("gpt-5.6-sol", "codex", 0, r.in, r.out, r.cacheRead, 0)
		if got.Source != CostSourceTable {
			t.Fatalf("row %d: source = %q, want %q", i, got.Source, CostSourceTable)
		}
		if diff := got.USD - wantPerRow[i]; diff > 0.0000005 || diff < -0.0000005 {
			t.Errorf("row %d: USD = %.6f, want %.6f", i, got.USD, wantPerRow[i])
		}
		total += got.USD
	}
	if diff := total - wantTotal; diff > 0.0000005 || diff < -0.0000005 {
		t.Errorf("issue total = %.6f, want %.6f", total, wantTotal)
	}
}
