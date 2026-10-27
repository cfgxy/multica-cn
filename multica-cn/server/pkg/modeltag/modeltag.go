// Package modeltag is the single Go rule source for the trailing
// context-window variant tag Claude Code appends to model ids — the `[1m]`
// in `claude-opus-5[1m]`. Before this package the shape lived as independent
// regex literals on each consumer (pkg/agent's claudeContextTagRe for the
// CLI launch arg, internal/metrics' contextTagRe plus inline bracket
// alternatives for billing), cross-referencing each other by comment only,
// so the launch and billing sides could silently drift apart; RUYI-267
// converged them here. The frontend keeps its own copy of necessity —
// `stripContextTag` in packages/views/runtimes/utils.ts is TypeScript — and
// parity with it is pinned by tests on both sides, not by shared code.
package modeltag

import "regexp"

// Pattern is the tag shape without any anchor: one complete bracket group
// with at least one character inside. Embed it inside a larger alternation
// (claudeVersionEnd, the qwen alias rules in internal/metrics/pricing.go)
// exactly as-is.
const Pattern = `\[[^\]]+\]`

// AtEnd is Pattern anchored at the end of the id — the form a standalone
// strip or match uses.
const AtEnd = Pattern + `$`

// Empty tags (`model[]`), unterminated ones (`model[`) and tags not at the
// end (`model[1m]-preview`) are not tags under this shape, and every
// consumer treats them as opaque id text. The shape is deliberately "one
// complete bracket group at the very end" and nothing else — the same shape
// the frontend recognises.

var atEndRe = regexp.MustCompile(AtEnd)

// Match reports whether model ends with a well-formed context tag.
func Match(model string) bool {
	return atEndRe.MatchString(model)
}

// StripOne removes exactly ONE trailing tag from model.
//
// changed reports whether a tag was removed at all; stillTagged — meaningful
// only when changed — reports whether the remainder still ends with a tag. A
// doubly tagged id (`gpt-5.6-sol[1m][2m]`) is not a shape Multica
// recognises, and every consumer treats stillTagged as "leave the id
// alone": peeling one tag off a doubly tagged id would forward a
// still-tagged id to the CLI as if it were deliberate (launch) or price a
// shape the dashboard leaves unmapped (billing).
//
// The single-pass rule is the contract both consumers agreed on in RUYI-256:
// claudeCLIModelArg and PriceForModelAlias strip exactly one tag, no more,
// so an id either is a tagged shape we recognise or passes through verbatim.
func StripOne(model string) (stripped string, changed, stillTagged bool) {
	stripped = atEndRe.ReplaceAllString(model, "")
	if stripped == model {
		return model, false, false
	}
	return stripped, true, atEndRe.MatchString(stripped)
}
