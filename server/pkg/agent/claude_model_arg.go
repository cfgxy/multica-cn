package agent

import (
	"regexp"
	"strings"
)

// claudeContextTagRe matches a trailing context-window variant tag such as the
// `[1m]` Claude Code's model catalog appends to a model id. One complete tag
// with at least one character inside, anchored at the end — the same shape
// `contextTagRe` in server/internal/metrics/pricing.go and `stripContextTag`
// in packages/views/runtimes/utils.ts recognise, so an empty tag (`model[]`)
// or a bare bracket (`model[`) is not a tag on any of the three sides.
var claudeContextTagRe = regexp.MustCompile(`\[[^\]]+\]$`)

// claudeCLIModelArg renders a model id the way the Claude CLI's `--model`
// accepts it.
//
// The catalog rows list_models returns carry the context-window tag on
// purpose (see claudeModelsFromInfos): `claude-opus-5[1m]` is literally what
// the CLI runs for the "Opus (1M context)" row, so forwarding the tag is what
// keeps an agent on the window it picked. A gateway alias is the opposite
// case — there the tag is a window marker the gateway never coined, and
// forwarding it makes the CLI reject the launch before any turn runs
// (`[claude-code:unrecognized_model] {"model":"gpt-5.6-sol[1m]"}`, RUYI-256),
// so the bare id is what goes on the wire.
//
// The Claude-vs-gateway split is the substring test the pricing table already
// keys its `anthropic:` rows on (modelAliasRules in
// server/internal/metrics/pricing.go): every Anthropic row matches a
// `claude-…` substring and no other vendor's row does. Matching that test
// here keeps one id from being a Claude model for billing and a gateway alias
// for launching.
//
// Exactly ONE tag is stripped, the same single pass PriceForModelAlias and
// the frontend's canonicalCandidates make. A doubly tagged id is not a shape
// we recognise, and peeling one tag off it would put a still-tagged id on the
// wire as if it were deliberate; it goes through verbatim and the CLI reports
// it.
func claudeCLIModelArg(model string) string {
	if strings.Contains(strings.ToLower(model), "claude") {
		return model
	}
	stripped := claudeContextTagRe.ReplaceAllString(model, "")
	if stripped == model || claudeContextTagRe.MatchString(stripped) {
		return model
	}
	return stripped
}
