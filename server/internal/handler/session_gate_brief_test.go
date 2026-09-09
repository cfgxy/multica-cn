package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// Database-backed contracts for the session context gate's brief (RUYI-107).
//
// The pure rendering rules live in session_gate_test.go; what needs a database
// here is which rows the brief picks and how the claim path combines the brief
// with the MUL-5305 continuity disclosure. Both were wrong in ways no unit test
// on the renderer could see.

// seedGatedFollowUp sets up an (agent, issue) pair whose most recent terminal
// task is over the context ceiling, plus a queued follow-up ready to claim. It
// returns the issue so the caller can hang comments off it.
//
// The agent's ceiling is the frozen minimum and the reading is above the hard
// limit, so the gate always decides to compact — these tests are about what the
// brief then contains, not about where the threshold sits.
func seedGatedFollowUp(t *testing.T, ctx context.Context, title string) (issueID, agentID, runtimeID, daemonID, priorTaskID string) {
	t.Helper()

	agentID, runtimeID, daemonID = createRuntimeGuardAgent(t, ctx)
	dbfx.Exec(t, `
		UPDATE agent SET session_max_context_tokens = 100000, session_compact_pct = 80 WHERE id = $1
	`, agentID)

	issueID = dbfx.Issue(t, title)

	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, session_id, work_dir, result
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '2 minutes',
		        'BRIEF-PRIOR-SESSION', '/tmp/brief-prior', '{"output":"previous run reported this"}'::jsonb)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&priorTaskID)

	// Over the hard ceiling, so the gate compacts rather than resumes.
	dbfx.Exec(t, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens,
		                        cache_read_tokens, cache_write_tokens, context_tokens)
		VALUES ($1, 'anthropic', 'brief-test-model', 0, 0, 0, 0, 150000)
	`, priorTaskID)

	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, issueID)

	return issueID, agentID, runtimeID, daemonID, priorTaskID
}

// TestClaimBrief_AnchorsRealRecentComments is the regression for the shape the
// first implementation had: it cut to the newest ROOTS and then labelled each
// root with its thread's newest timestamp, so an old thread that just received
// the decisive reply was dropped entirely, and the roots that survived were
// attributed to a comment nobody wrote.
func TestClaimBrief_AnchorsRealRecentComments(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	issueID, _, runtimeID, daemonID, _ := seedGatedFollowUp(t, ctx, "brief anchors recent comments")

	// An OLD root — old enough that a cut on root creation time drops it —
	// whose thread just received the reply that matters.
	oldRoot := dbfx.Comment(t, issueID, "the long-running question", testutil.Cols{
		"created_at": testutil.Raw("now() - interval '40 days'"),
	})
	decisiveReply := dbfx.Comment(t, issueID, "the decisive answer nobody must lose", testutil.Cols{
		"parent_id":  oldRoot,
		"created_at": testutil.Raw("now() - interval '1 minute'"),
	})

	// Newer roots that would fill the whole window on their own.
	for i := 0; i < priorContextBriefRecent+4; i++ {
		dbfx.Comment(t, issueID, "filler root", testutil.Cols{
			"created_at": testutil.Raw("now() - interval '10 days'"),
			// Resolved, so they compete for the recent-comment window without
			// also crowding the unresolved section this test checks.
			"resolved_at":      testutil.Raw("now()"),
			"resolved_by_type": "member",
			"resolved_by_id":   testUserID,
		})
	}

	brief := claimBriefForTest(t, runtimeID, daemonID)

	if !strings.Contains(brief, shortIDOf(decisiveReply)) {
		t.Errorf("the newest comment must be anchored even though its root is old.\nbrief:\n%s", brief)
	}
	// The reply's line points at the ROOT for `--thread`, so both ids appear —
	// but the reply's own id is what identifies the comment being quoted.
	if !strings.Contains(brief, "in thread `"+shortIDOf(oldRoot)+"`") {
		t.Errorf("a reply anchor must name the thread it belongs to.\nbrief:\n%s", brief)
	}
	if !strings.Contains(brief, "the decisive answer nobody must lose") {
		t.Errorf("the excerpt must come from the anchored comment itself.\nbrief:\n%s", brief)
	}
	// The unresolved section still answers "what is still open", and the old
	// root qualifies because its thread's real activity is a minute ago.
	if !strings.Contains(brief, "the long-running question") {
		t.Errorf("an unresolved thread ranked by real activity must survive the row limit.\nbrief:\n%s", brief)
	}
}

// TestClaimBrief_ExcludesReplyResolvedThreads pins the thread-level reading of
// "unresolved" the rest of the repository already uses (deriveThreadResolution /
// foldResolvedThreads): a thread is resolved when its root is resolved OR when
// any reply carries the resolution. Selecting candidates on the root's own
// resolved_at alone put a thread whose conclusion was recorded on a reply — the
// normal shape of a settled discussion — back in front of a fresh session as an
// open question, so the run would reopen decisions that were already made.
func TestClaimBrief_ExcludesReplyResolvedThreads(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	issueID, _, runtimeID, daemonID, _ := seedGatedFollowUp(t, ctx, "brief excludes reply-resolved threads")

	// Settled thread: the root was never resolved, the reply that concluded it
	// was. This is the case the first implementation got wrong.
	settledRoot := dbfx.Comment(t, issueID, "the question that was already answered", testutil.Cols{
		"created_at": testutil.Raw("now() - interval '3 days'"),
	})
	dbfx.Comment(t, issueID, "and here is the decision that closed it", testutil.Cols{
		"parent_id":        settledRoot,
		"created_at":       testutil.Raw("now() - interval '2 days'"),
		"resolved_at":      testutil.Raw("now() - interval '2 days'"),
		"resolved_by_type": "member",
		"resolved_by_id":   testUserID,
	})

	// Control: a thread with a reply but no resolution anywhere must still be
	// listed, so the fix cannot pass by emptying the section.
	openRoot := dbfx.Comment(t, issueID, "the question still waiting on an answer", testutil.Cols{
		"created_at": testutil.Raw("now() - interval '3 days'"),
	})
	dbfx.Comment(t, issueID, "still thinking about it", testutil.Cols{
		"parent_id":  openRoot,
		"created_at": testutil.Raw("now() - interval '1 day'"),
	})

	section := unresolvedSectionOf(t, claimBriefForTest(t, runtimeID, daemonID))

	if strings.Contains(section, "the question that was already answered") {
		t.Errorf("a thread resolved by a reply must not be listed as unresolved.\nUnresolved threads:\n%s", section)
	}
	if !strings.Contains(section, "the question still waiting on an answer") {
		t.Errorf("a thread with no resolution anywhere must stay listed.\nUnresolved threads:\n%s", section)
	}
}

// unresolvedSectionOf returns just the brief's "Unresolved threads" section.
// The assertions have to read that section alone: a root excluded from it is
// still free to appear under "Most recent comments", which is a different claim
// about the same comment.
func unresolvedSectionOf(t *testing.T, brief string) string {
	t.Helper()

	const header = "### Unresolved threads\n"
	start := strings.Index(brief, header)
	if start < 0 {
		t.Fatalf("the brief has no unresolved-threads section at all.\nbrief:\n%s", brief)
	}
	rest := brief[start+len(header):]
	if end := strings.Index(rest, "###"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestClaimBrief_DisclosesContinuityGapWhenCompacting is the MUL-5305
// regression the gate reintroduced: a follow-up whose most recent terminal task
// withheld its session falls back to an OLDER session, and if that older session
// is also over the ceiling the gate compacts it. The compaction must not swallow
// the gap — the run would otherwise be told "here is your hand-off" for a turn
// whose context never existed.
func TestClaimBrief_DisclosesContinuityGapWhenCompacting(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	dbfx.Exec(t, `
		UPDATE agent SET session_max_context_tokens = 100000, session_compact_pct = 80 WHERE id = $1
	`, agentID)

	issueID := dbfx.Issue(t, "brief discloses the continuity gap")
	dbfx.Comment(t, issueID, "an open question the fresh session still has to answer")

	// The older turn owns the only resumable session, and it is oversized.
	var olderTaskID string
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, session_id, work_dir, result
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '5 minutes', now() - interval '5 minutes',
		        'OLD-BUT-OVERSIZED', '/tmp/old', '{"output":"the older turn"}'::jsonb)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&olderTaskID)
	dbfx.Exec(t, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens,
		                        cache_read_tokens, cache_write_tokens, context_tokens)
		VALUES ($1, 'anthropic', 'brief-test-model', 0, 0, 0, 0, 150000)
	`, olderTaskID)

	// The most recent turn withheld its session (rollout missing). Its result is
	// the one the brief must carry, and its gap is the one it must disclose.
	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, work_dir, result, session_rollout_missing
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 minute', now() - interval '1 minute',
		        '/tmp/newer', '{"output":"the newest turn, whose session was withheld"}'::jsonb, TRUE)
	`, agentID, runtimeID, issueID)

	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, issueID)

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)

	if task.PriorSessionID != "" {
		t.Fatalf("the oversized fallback session must not be resumed, got %q", task.PriorSessionID)
	}
	if task.PriorContextBrief == "" {
		t.Fatal("a compacting claim must attach a brief")
	}
	if !strings.Contains(task.PriorContextBrief, "could not have its context carried over") {
		t.Errorf("the brief must disclose the real continuity gap.\nbrief:\n%s", task.PriorContextBrief)
	}
	// The brief's result section describes the MOST RECENT terminal task, not
	// the older task the fallback session came from — reading the two from
	// different rows is how the first implementation described two turns as one.
	if !strings.Contains(task.PriorContextBrief, "the newest turn, whose session was withheld") {
		t.Errorf("the brief must carry the most recent turn's result.\nbrief:\n%s", task.PriorContextBrief)
	}
	// Brief and plain notice are mutually exclusive: sending both tells the
	// daemon "here is your context" and "nothing carried over" at once.
	if task.PriorSessionResumeUnavailable {
		t.Error("a claim that attached a brief must not also raise the plain continuity notice")
	}
}

// TestClaimBrief_GapNoticeSurvivesWithoutCompaction pins the other half of the
// same exclusivity: when the gate does NOT compact, the plain MUL-5305 notice is
// still the only disclosure there is and must stay raised.
func TestClaimBrief_GapNoticeSurvivesWithoutCompaction(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	issueID := dbfx.Issue(t, "gap notice without compaction")

	// A healthy older session — no usage row at all, so the reading is unknown
	// and decision D4 A resumes it.
	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '5 minutes', now() - interval '5 minutes',
		        'OLD-AND-HEALTHY', '/tmp/old')
	`, agentID, runtimeID, issueID)
	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, work_dir, session_rollout_missing
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 minute', now() - interval '1 minute',
		        '/tmp/newer', TRUE)
	`, agentID, runtimeID, issueID)
	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, issueID)

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)

	if task.PriorSessionID != "OLD-AND-HEALTHY" {
		t.Fatalf("an unknown reading must still resume, got %q", task.PriorSessionID)
	}
	if task.PriorContextBrief != "" {
		t.Errorf("a resuming claim must not attach a brief, got %q", task.PriorContextBrief)
	}
	if !task.PriorSessionResumeUnavailable {
		t.Error("the MUL-5305 continuity gap must still be disclosed when the gate does not compact")
	}
}

// claimBriefForTest claims the queued follow-up and returns the brief it was
// handed, failing when the gate did not compact.
func claimBriefForTest(t *testing.T, runtimeID, daemonID string) string {
	t.Helper()

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorContextBrief == "" {
		t.Fatalf("expected the gate to compact and attach a brief, got prior_session_id=%q unavailable=%v",
			task.PriorSessionID, task.PriorSessionResumeUnavailable)
	}
	return task.PriorContextBrief
}

// shortIDOf is the 8-character handle the brief anchors comments by.
func shortIDOf(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
