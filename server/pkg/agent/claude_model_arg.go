package agent

import (
	"strings"

	"github.com/multica-ai/multica/server/pkg/modeltag"
)

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
// Exactly ONE tag is stripped, through modeltag.StripOne — the shared rule
// source PriceForModelAlias uses, so launch and billing cannot disagree about
// which shapes carry a tag. A doubly tagged id is not a shape we recognise,
// and peeling one tag off it would put a still-tagged id on the wire as if it
// were deliberate; it goes through verbatim and the CLI reports it.
//
// Out-of-catalog gateway aliases (RUYI-267): a bare id the installed CLI
// does not list — `gpt-5.6-sol` behind a routing gateway — goes on the wire
// verbatim, and the CLI may print its own
// `[claude-code:unrecognized_model]` stderr notice while still running the
// model. That notice is the CLI's, and its observability level is the stderr
// relay's: every line reaches the daemon log at Debug (the `[claude:stderr]`
// prefix) and a bounded tail rides on the final error when a run fails. No
// Multica code classifies or filters it, and none should grow here — quiet
// relay is the contract, and silencing the line would also silence the
// launch-blocking form the tag-stripping case above depends on seeing.
func claudeCLIModelArg(model string) string {
	if strings.Contains(strings.ToLower(model), "claude") {
		return model
	}
	stripped, changed, stillTagged := modeltag.StripOne(model)
	if !changed || stillTagged {
		return model
	}
	return stripped
}
