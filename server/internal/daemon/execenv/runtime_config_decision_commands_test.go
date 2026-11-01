package execenv

import (
	"strings"
	"testing"
)

// Decision cards are the only supported channel for a choice that changes
// Owner-set scope, acceptance, authorization, or irreversible tradeoffs. The
// brief's command list previously mentioned only `comment add`, and an agent
// that needed Owner authorization posted an ordinary comment as a "decision
// card" instead (RUYI-914/RUYI-915). Keep both the command and the
// plain-comment prohibition discoverable.
func TestAvailableCommandsDiscoverDecisionCardCLI(t *testing.T) {
	t.Parallel()
	out := buildMetaSkillContent("claude", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	})
	for _, want := range []string{
		"multica issue decision list <issue-id>",
		"multica issue decision add <issue-id>",
		"Repeat `--option` 2–4 times",
		"never replace it with a plain comment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("runtime brief missing %q", want)
		}
	}
}
