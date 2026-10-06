package daemon

import (
	"strings"
	"testing"
)

// RUYI-425 stage 4 (design §3.5): voice write-back reaches Claude Code
// through the PriorContextBrief, and the brief rides the per-turn blocks
// (MUL-5377) — so the voice section must survive into the assembled turn.
// The gateway/db side proves the section exists; this pins the daemon-side
// leg: a brief carrying it lands in perTurnContextBlocks verbatim.
func TestPerTurnBlocksCarryVoiceBriefSection(t *testing.T) {
	t.Parallel()

	const voiceSection = "### Recent voice session\n\n" +
		"The summary is a projection rebuilt from the fact layer; the fact events under it are the authoritative record.\n\n" +
		"Voice session live_session:abc (2 turns), 1 fact event(s):\n- [user_decision] user decision: 决定用 Postgres\n\n" +
		"Fact events (cite by id):\n- `live_session:abc#turn:1#user_decision` [user_decision] evidence=live_session:abc#turn:1"
	brief := "## Prior Session Context\n\nhand-off...\n\n" + voiceSection + "\n"

	blocks := perTurnContextBlocks(Task{
		IssueID:           "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		PriorContextBrief: brief,
	}, promptOpts{})
	for _, want := range []string{
		"### Recent voice session",
		"`live_session:abc#turn:1#user_decision`",
		"决定用 Postgres",
	} {
		if !strings.Contains(blocks, want) {
			t.Errorf("per-turn blocks lost %q from the brief:\n%s", want, blocks)
		}
	}
}
