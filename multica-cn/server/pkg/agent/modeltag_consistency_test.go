package agent

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/modeltag"
)

// The tests in this file are the RUYI-267 anti-drift suite. The tag-shape
// rule itself converged onto pkg/modeltag (single Go source for the launch
// and billing sides); what can still drift is the *relationship between the
// consumers*: launch folding (claudeCLIModelArg) versus billing folding
// (metrics.PriceForModelAlias), and the deliberately narrower capability
// shape (claudeContextWindowTagRe in models.go). Each test pins one of those
// relationships so a future shape change has to break a named test, not a
// comment.

// TestLaunchAndBillingFoldTaggedIdsTheSameWay pins the invariant that the id
// placed on the wire is billed the same way as the id configured: for every
// case, metrics.PriceForModelAlias(model) and
// metrics.PriceForModelAlias(claudeCLIModelArg(model)) agree — same SKU when
// the id is priced, both unmapped when it is not — and the wire value equals
// wantWire. A change on either side that breaks this (launch peeling two
// tags, billing dropping its single-tag retry) lands here first.
func TestLaunchAndBillingFoldTaggedIdsTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// wantUnmapped marks ids billing must refuse; every other case
		// asserts the same Provider/Model SKU for the configured and the
		// wire id.
		wantUnmapped bool
		model        string
		wantWire     string
	}{
		// Tagged gateway aliases: launch strips the tag, billing prices
		// the tagged and the bare id identically.
		{model: "gpt-5.6-sol[1m]", wantWire: "gpt-5.6-sol"},
		{model: "gpt-5.6-terra[1m]", wantWire: "gpt-5.6-terra"},
		{model: "kimi-k3[1m]", wantWire: "kimi-k3"},
		{model: "grok-4.5[1m]", wantWire: "grok-4.5"},
		{model: "qwen3.8-max[1m]", wantWire: "qwen3.8-max"},
		{model: "moonshotai/kimi-k3[1m]", wantWire: "moonshotai/kimi-k3"},

		// Bare out-of-catalog aliases: verbatim on the wire (the CLI's
		// own unrecognized_model notice is the upstream contract), priced
		// the same either way.
		{model: "gpt-5.6-sol", wantWire: "gpt-5.6-sol"},

		// Tagged Claude rows: the tag is the CLI's own window marker, so
		// it rides along AND billing accepts it (claudeVersionEnd).
		{model: "claude-opus-5[1m]", wantWire: "claude-opus-5[1m]"},
		{model: "claude-fable-5-1[1m]", wantWire: "claude-fable-5-1[1m]"},

		// Unrecognised shapes: both sides refuse — launch forwards
		// verbatim, billing leaves the id unmapped.
		{wantUnmapped: true, model: "gpt-5.6-sol[1m][2m]", wantWire: "gpt-5.6-sol[1m][2m]"},
		{wantUnmapped: true, model: "gpt-5.6-sol[]", wantWire: "gpt-5.6-sol[]"},
		{wantUnmapped: true, model: "gpt-5.6-sol[1m", wantWire: "gpt-5.6-sol[1m"},
		{wantUnmapped: true, model: "gpt-5.6-sol[1m]-preview", wantWire: "gpt-5.6-sol[1m]-preview"},

		// An alias no price row covers: shape normalization still applies
		// on the wire, and configured and wire id are unmapped together.
		{wantUnmapped: true, model: "no-such-vendor/no-such-model[1m]", wantWire: "no-such-vendor/no-such-model"},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()

			wire := claudeCLIModelArg(tc.model)
			if wire != tc.wantWire {
				t.Fatalf("claudeCLIModelArg(%q) = %q, want %q", tc.model, wire, tc.wantWire)
			}

			configuredPrice, configuredOK := metrics.PriceForModelAlias(tc.model)
			wirePrice, wireOK := metrics.PriceForModelAlias(wire)
			if tc.wantUnmapped {
				if configuredOK || wireOK {
					t.Fatalf("model %q: expected both configured and wire id unmapped, got (%v, %v) and (%v, %v)",
						tc.model, configuredPrice, configuredOK, wirePrice, wireOK)
				}
				return
			}
			if !configuredOK || !wireOK {
				t.Fatalf("model %q: expected both configured and wire id priced, got configured ok=%v wire ok=%v",
					tc.model, configuredOK, wireOK)
			}
			if configuredPrice.Provider != wirePrice.Provider || configuredPrice.Model != wirePrice.Model {
				t.Fatalf("model %q: configured id bills as %s/%s but wire id %q bills as %s/%s",
					tc.model,
					configuredPrice.Provider, configuredPrice.Model,
					wire, wirePrice.Provider, wirePrice.Model)
			}
		})
	}
}

// TestContextTagShapesStayNested pins the adjudicated relationship between
// the two tag shapes Multica intentionally keeps: capability lookup's
// claudeContextWindowTagRe (strict: positive size + k/m unit only) must
// always accept a subset of what pkg/modeltag's shared launch/billing shape
// accepts. Strictness narrowing is a capability-lookup-only decision; the
// moment the strict shape accepts something the shared shape rejects, a
// tagged id would normalize for capabilities but stay tagged for launch and
// billing — exactly the split this suite exists to prevent.
func TestContextTagShapesStayNested(t *testing.T) {
	t.Parallel()

	bases := []string{"claude-opus-5", "claude-fable-5-1", "gpt-5.6-sol"}
	suffixes := []string{
		"[1m]", "[200k]", "[9m]", "[123456k]", // real size tags
		"[foo]", "[]", "[1m", "[1m][2m]", // non-size or malformed
	}

	for _, base := range bases {
		for _, suffix := range suffixes {
			id := base + suffix
			strict := claudeContextWindowTagRe.MatchString(id)
			lenient := modeltag.Match(id)
			if strict && !lenient {
				t.Fatalf("id %q: strict capability shape accepts what the shared modeltag shape rejects", id)
			}
		}
	}

	// The discriminating pairs, spelled out: genuine size tags are in
	// both shapes, everything else is lenient-only.
	for _, id := range []string{"claude-opus-5[1m]", "claude-opus-5[200k]"} {
		if !claudeContextWindowTagRe.MatchString(id) || !modeltag.Match(id) {
			t.Fatalf("id %q: expected a match in both shapes", id)
		}
	}
	// `[foo]` is the discriminator: a complete bracket tag the shared
	// shape accepts but the strict capability shape must not.
	if claudeContextWindowTagRe.MatchString("claude-opus-5[foo]") {
		t.Fatalf("id %q: strict capability shape must reject a non-size modifier", "claude-opus-5[foo]")
	}
	if !modeltag.Match("claude-opus-5[foo]") {
		t.Fatalf("id %q: expected the shared shape to accept a complete bracket tag", "claude-opus-5[foo]")
	}
	for _, id := range []string{"claude-opus-5[]", "claude-opus-5[1m"} {
		if claudeContextWindowTagRe.MatchString(id) || modeltag.Match(id) {
			t.Fatalf("id %q: expected a reject in both shapes", id)
		}
	}
}

// TestModelIDForCapabilityLookupTagMatrix runs the four id categories
// (RUYI-267) through the capability normalization. The rule is
// provider-scoped, not model-family-scoped: on the claude provider a
// trailing size tag is by definition the CLI's own window marker, so it is
// stripped wherever it appears; on every other provider the id is catalog
// text and stays untouched.
func TestModelIDForCapabilityLookupTagMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		provider string
		model    string
		want     string
	}{
		// Catalog models, untagged: identity.
		{"claude", "claude-opus-5", "claude-opus-5"},
		// Tagged with a genuine context size: stripped.
		{"claude", "claude-opus-5[1m]", "claude-opus-5"},
		{"claude", "claude-opus-5[200k]", "claude-opus-5"},
		// Bracket text that is not a context size: exact match, never
		// inherits the base model's capabilities.
		{"claude", "claude-opus-5[foo]", "claude-opus-5[foo]"},
		{"claude", "claude-opus-5[]", "claude-opus-5[]"},
		{"claude", "claude-opus-5[1m", "claude-opus-5[1m"},
		{"claude", "claude-opus-5[1m]-preview", "claude-opus-5[1m]-preview"},
		// A doubly tagged id is not a shape the shared rule recognises,
		// but this normalizer is a best-effort lookup key, not a
		// validator: it peels the trailing size tag and looks up the
		// remainder. Pre-existing behavior, pinned as-is.
		{"claude", "claude-opus-5[1m][2m]", "claude-opus-5[1m]"},
		// The rule keys on the provider: a gateway alias on the claude
		// provider is normalized like any claude id, and a tagged id on
		// any other provider is untouched.
		{"claude", "gpt-5.6-sol[1m]", "gpt-5.6-sol"},
		{"codex", "gpt-5.6-sol[1m]", "gpt-5.6-sol[1m]"},
	}

	for _, tc := range cases {
		if got := modelIDForCapabilityLookup(tc.provider, tc.model); got != tc.want {
			t.Fatalf("modelIDForCapabilityLookup(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}
