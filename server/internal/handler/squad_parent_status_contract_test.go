package handler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// The two tests below are composition tests, not text-presence tests. The
// parent-status contract is split across two systems that never see each
// other: the squad briefing (server-side, appended to the leader's
// Instructions) and the runtime brief (daemon-side CLAUDE.md). Asserting each
// half in isolation is exactly how the original contradiction shipped — the
// briefing said "move it to in_review when the goal is met" while the runtime
// brief said "do not change status unless the comment asks", and a member's
// delivery comment never asks.
//
// So each test assembles both halves for one real scenario and asserts the
// combined instruction set points one way.

// leaderCommentSurfaces renders BOTH surfaces a squad leader receives on a
// comment-triggered turn, the way the daemon actually assembles them since
// MUL-5442 #6493 round 3: the cached brief gets only the STABLE half of the
// instructions (the briefing is split off so the brief stays byte-identical
// across leader/worker turns), and the per-turn prompt carries the full
// briefing plus the leader rules block.
func leaderCommentSurfaces(t *testing.T, instructions string) (brief, prompt string) {
	t.Helper()
	stable, _ := execenv.SplitSquadBriefing(instructions)
	dir := t.TempDir()
	if _, err := execenv.InjectRuntimeConfig(dir, "claude", execenv.TaskContextForEnv{
		IssueID:           "issue-1",
		TriggerCommentID:  "comment-1",
		AgentInstructions: stable,
		IsSquadLeader:     true,
	}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	prompt = daemon.BuildPrompt(daemon.Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "done — all subtasks finished",
		TriggerAuthorType:     "member",
		Agent:                 &daemon.AgentData{Name: "Lead", Instructions: instructions},
	}, "claude")
	return string(data), prompt
}

// workerRuntimeBrief renders the CLAUDE.md the same agent receives on a
// non-leader turn (plain instructions, no briefing appended).
func workerRuntimeBrief(t *testing.T, stableInstructions string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := execenv.InjectRuntimeConfig(dir, "claude", execenv.TaskContextForEnv{
		IssueID:           "issue-1",
		TriggerCommentID:  "comment-1",
		AgentInstructions: stableInstructions,
	}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	return string(data)
}

// TestSquadAssignedLeaderCanWrapUpOnCommentTurn covers the squad's most common
// shape: work dispatched by @mention with no child issues, so no child-done
// system comment ever arrives to carry an explicit status ask. The member
// simply posts "done". The leader must still be able to close the parent out.
func TestSquadAssignedLeaderCanWrapUpOnCommentTurn(t *testing.T) {
	ctx := context.Background()
	leaderID, _ := seededLeaderAgent(t)
	squad := seedSquadForBriefing(t, leaderID, "Owning Squad", "")

	// The issue is assigned to this squad → the server grants status ownership.
	briefing := buildSquadLeaderBriefing(ctx, testHandler.Queries, squad, true)
	stableInstr := "Coordinate the team."
	combinedInstr := stableInstr + "\n\n" + briefing
	brief, prompt := leaderCommentSurfaces(t, combinedInstr)

	if !strings.Contains(briefing, "Own the parent issue status") {
		t.Fatalf("squad-assigned briefing must grant status ownership:\n%s", briefing)
	}

	// MUL-5442 #6493 round 3: the brief is role-independent — byte-identical
	// to the worker-turn render — so the grant carve-out and the briefing
	// itself must arrive via the per-turn prompt instead.
	if worker := workerRuntimeBrief(t, stableInstr); brief != worker {
		t.Error("leader-turn brief diverges from the worker-turn brief — the squad briefing leaked into the cached surface")
	}
	for _, want := range []string{
		// The carve-out must name the granting section, not gesture at it —
		// the leader has to be able to tell whether it applies to this turn.
		`"Own the parent issue status"`,
		"treat it as a standing grant",
		"without waiting to be asked",
		// The full briefing itself rides the prompt.
		"## Squad Operating Protocol",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("leader per-turn prompt missing %q\n--- prompt ---\n%s", want, prompt)
		}
	}

	// End to end: the surfaces the leader actually sees must agree that
	// in_review is reachable here (the wrap-up command arrives with the
	// briefing inside the prompt).
	combined := brief + "\n" + prompt
	if !strings.Contains(combined, "multica issue status <issue-id> in_review") {
		t.Error("combined instructions never tell the owning leader how to wrap up")
	}
}

// TestGuestLeaderCannotChangeStatusOnCommentTurn is the other half of the
// scope fix (MUL-3724 path): the issue belongs to a plain agent and this squad
// was only @mentioned for help. The briefing still gets injected — the leader
// needs its roster — but no combination of the two halves may authorize a
// status change on someone else's issue.
func TestGuestLeaderCannotChangeStatusOnCommentTurn(t *testing.T) {
	ctx := context.Background()
	leaderID, _ := seededLeaderAgent(t)
	squad := seedSquadForBriefing(t, leaderID, "Guest Squad", "")

	// The issue is assigned to someone else → no status ownership.
	briefing := buildSquadLeaderBriefing(ctx, testHandler.Queries, squad, false)
	brief, prompt := leaderCommentSurfaces(t, "Coordinate the team.\n\n"+briefing)

	// The leader still gets the coordination context it was pulled in for —
	// withholding status authority must not withhold the roster too.
	for _, want := range []string{
		"## Squad Roster",
		"Leader (you):",
		"Delegate by @mention",
		"Record your evaluation",
	} {
		if !strings.Contains(briefing, want) {
			t.Fatalf("guest leader lost coordination context %q:\n%s", want, briefing)
		}
	}

	// But the grant is absent, so the runtime brief's carve-out has nothing to
	// activate and the default prohibition governs.
	if strings.Contains(briefing, "Own the parent issue status") {
		t.Errorf("guest leader must not receive the status-ownership grant:\n%s", briefing)
	}
	// #6493 round 3: compose what the guest leader actually sees — the
	// role-independent brief plus the per-turn prompt (which carries the
	// guest briefing and the leader rules). No surface may hand it a
	// runnable in_review command.
	combined := brief + "\n" + prompt
	if strings.Contains(combined, "multica issue status <issue-id> in_review") {
		t.Error("combined instructions hand a guest leader an in_review command for " +
			"an issue assigned to someone else")
	}
	// The prohibition wraps across source lines, so match on compacted text.
	compact := strings.Join(strings.Fields(briefing), " ")
	for _, want := range []string{
		"Do NOT change this issue's status",
		"never run `multica issue status` on it",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("guest-leader briefing missing %q\n--- briefing ---\n%s", want, briefing)
		}
	}
}
