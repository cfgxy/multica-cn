package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-667: server-side suppression of same-issue agent self-triggers. A
// comment authored by an agent whose trigger target is the author itself is
// suppressed WHEN the author has an active run on the comment's issue; a
// cross-issue self-wake (parent-handoff wake) stays admitted. Suppression is
// visible: the mention target surfaces as blocked/self_trigger_suppressed,
// never a silent drop. The whole feature sits behind
// cfg.SelfTriggerSuppressionEnabled; off means byte-for-byte today's behavior.

// --- pure decision core -------------------------------------------------

func selfTriggerAuthorID(n int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
}

func selfTriggerFixture() (author, other string) {
	return selfTriggerAuthorID(1), selfTriggerAuthorID(2)
}

func TestFilterSameIssueSelfTriggers_SuppressesVisibleTargets(t *testing.T) {
	// Acceptance #1: a same-issue self mention is suppressed AND visible —
	// the trigger is removed and the mention target carries
	// blocked/self_trigger_suppressed, never a silent drop.
	author, other := selfTriggerFixture()
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceMentionAgent},
		{Agent: db.Agent{ID: parseUUID(other)}, Source: commentTriggerSourceMentionAgent},
	}
	targets := []commentMentionTarget{
		{TargetType: "agent", TargetID: author, ExecAgentID: author},
		{TargetType: "agent", TargetID: other, ExecAgentID: other},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, targets, author, true, true)

	if len(gotTriggers) != 1 || uuidToString(gotTriggers[0].Agent.ID) != other {
		t.Fatalf("triggers = %+v, want only the non-author trigger", gotTriggers)
	}
	if len(gotTargets) != 2 {
		t.Fatalf("targets = %+v, want both preserved", gotTargets)
	}
	suppressed := gotTargets[0]
	if suppressed.Status != DispatchBlocked || suppressed.ReasonCode != ReasonSelfTriggerSuppressed {
		t.Errorf("author target = %+v, want blocked/self_trigger_suppressed (visible, not dropped)", suppressed)
	}
	if suppressed.ExecAgentID != "" {
		t.Errorf("author target ExecAgentID = %q, want cleared so the outcome channel reports the block", suppressed.ExecAgentID)
	}
	if gotTargets[1].Status != "" || gotTargets[1].ReasonCode != "" {
		t.Errorf("other target = %+v, want untouched", gotTargets[1])
	}
}

func TestFilterSameIssueSelfTriggers_CrossIssueWakeAdmitted(t *testing.T) {
	// Acceptance #2: the author's active run is on a DIFFERENT issue than the
	// comment — the self-wake (parent-handoff channel) must stay admitted.
	author, other := selfTriggerFixture()
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceMentionAgent},
		{Agent: db.Agent{ID: parseUUID(other)}, Source: commentTriggerSourceMentionAgent},
	}
	targets := []commentMentionTarget{
		{TargetType: "agent", TargetID: author, ExecAgentID: author},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, targets, author, false, true)

	if len(gotTriggers) != 2 {
		t.Fatalf("triggers = %+v, want both kept (cross-issue self-wake is legal)", gotTriggers)
	}
	if gotTargets[0].Status != "" || gotTargets[0].ReasonCode != "" {
		t.Fatalf("targets = %+v, want untouched", gotTargets)
	}
}

func TestFilterSameIssueSelfTriggers_SwitchOffIsIdentity(t *testing.T) {
	// Acceptance #5: with the switch off the filter is an identity — exactly
	// today's behavior, the documented rollback path.
	author, _ := selfTriggerFixture()
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceMentionAgent},
	}
	targets := []commentMentionTarget{
		{TargetType: "agent", TargetID: author, ExecAgentID: author},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, targets, author, true, false)

	if len(gotTriggers) != 1 || len(gotTargets) != 1 || gotTargets[0].Status != "" {
		t.Fatalf("got (%+v, %+v), want inputs unchanged with the switch off", gotTriggers, gotTargets)
	}
}

func TestFilterSameIssueSelfTriggers_SquadExecTargetMarked(t *testing.T) {
	// A @squad mention whose leader IS the author resolves to the leader as
	// the executing agent: the suppression must mark the SQUAD target too,
	// otherwise the outcome channel would read the missing enqueue result as
	// "user opted out" and go silent (commentTriggerOutcomes contract).
	author, _ := selfTriggerFixture()
	squadID := selfTriggerAuthorID(9)
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceMentionSquadLeader},
	}
	targets := []commentMentionTarget{
		{TargetType: "squad", TargetID: squadID, ExecAgentID: author},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, targets, author, true, true)

	if len(gotTriggers) != 0 {
		t.Fatalf("triggers = %+v, want the leader trigger removed", gotTriggers)
	}
	if gotTargets[0].Status != DispatchBlocked || gotTargets[0].ReasonCode != ReasonSelfTriggerSuppressed || gotTargets[0].ExecAgentID != "" {
		t.Fatalf("squad target = %+v, want blocked/self_trigger_suppressed with ExecAgentID cleared", gotTargets[0])
	}
}

func TestFilterSameIssueSelfTriggers_ImplicitRouteRemovedWithoutTarget(t *testing.T) {
	// Implicit routing (assignee / thread-parent fallback) produces triggers
	// with NO mention targets: the filter removes the trigger; there is no
	// per-target outcome channel to mark, matching existing implicit no-route
	// semantics.
	author, _ := selfTriggerFixture()
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceIssueAssignee},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, nil, author, true, true)

	if len(gotTriggers) != 0 || gotTargets != nil {
		t.Fatalf("got (%+v, %+v), want the implicit self trigger removed with no targets touched", gotTriggers, gotTargets)
	}
}

func TestFilterSameIssueSelfTriggers_DoesNotOverrideExistingTerminalTarget(t *testing.T) {
	// Coexistence with the squad-leader heuristic: when the existing special
	// case already suppressed (no trigger added, target already terminal with
	// its own more precise outcome — e.g. deferred/already_active), the
	// generic filter must NOT rewrite that outcome.
	author, _ := selfTriggerFixture()
	squadID := selfTriggerAuthorID(9)
	targets := []commentMentionTarget{
		{TargetType: "squad", TargetID: squadID, Status: DispatchDeferred, ReasonCode: ReasonAlreadyActive},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(nil, targets, author, true, true)

	if len(gotTriggers) != 0 {
		t.Fatalf("triggers = %+v, want none", gotTriggers)
	}
	if gotTargets[0].Status != DispatchDeferred || gotTargets[0].ReasonCode != ReasonAlreadyActive {
		t.Fatalf("target = %+v, want the existing deferred/already_active outcome preserved", gotTargets[0])
	}
}

func TestFilterSameIssueSelfTriggers_CaseInsensitiveAuthorMatch(t *testing.T) {
	// The author id may arrive with different UUID casing than the canonical
	// uuidToString form; the comparison must not depend on it.
	author, _ := selfTriggerFixture()
	upper := ""
	for _, c := range author {
		if c >= 'a' && c <= 'f' {
			upper += string(c - 'a' + 'A')
		} else {
			upper += string(c)
		}
	}
	triggers := []commentAgentTrigger{
		{Agent: db.Agent{ID: parseUUID(author)}, Source: commentTriggerSourceMentionAgent},
	}
	targets := []commentMentionTarget{
		{TargetType: "agent", TargetID: author, ExecAgentID: author},
	}

	gotTriggers, gotTargets := filterSameIssueSelfTriggers(triggers, targets, upper, true, true)

	if len(gotTriggers) != 0 {
		t.Fatalf("triggers = %+v, want the self trigger suppressed despite casing", gotTriggers)
	}
	if gotTargets[0].Status != DispatchBlocked {
		t.Fatalf("target = %+v, want blocked", gotTargets[0])
	}
}

// --- wiring (DB-backed) -------------------------------------------------

func seedActiveTaskForSelfTrigger(t *testing.T, agentID, issueID string) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, started_at)
		VALUES ($1, $2, 'running', 0, $3, now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), issueID).Scan(&taskID); err != nil {
		t.Fatalf("seed active task: %v", err)
	}
	return taskID
}

func countSelfTriggerTasks(t *testing.T, issueID, agentID string) int {
	t.Helper()
	var total int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`,
		issueID, agentID).Scan(&total); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return total
}

func createSelfTriggerComment(t *testing.T, issueID, agentID, taskID, content string) CommentResponse {
	t.Helper()
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{"content": content}), "id", issueID)
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Task-ID", taskID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	return resp
}

func withSelfTriggerEnabled(t *testing.T, enabled bool) {
	t.Helper()
	previous := testHandler.cfg.SelfTriggerSuppressionEnabled
	testHandler.cfg.SelfTriggerSuppressionEnabled = enabled
	t.Cleanup(func() { testHandler.cfg.SelfTriggerSuppressionEnabled = previous })
}

// Acceptance #1 (end-to-end): an agent mid-run on issue X posts a comment on
// issue X mentioning itself → suppressed, visible as
// blocked/self_trigger_suppressed, and no new task.
func TestCreateComment_SameIssueAgentSelfMentionSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, true)

	agentID := createHandlerTestAgent(t, "Same-Issue Self-Mention Author", nil)
	issueID := createCommentTriggerPreviewIssue(t, "same-issue self mention", "", "")
	taskID := seedActiveTaskForSelfTrigger(t, agentID, issueID)

	resp := createSelfTriggerComment(t, issueID, agentID, taskID,
		fmt.Sprintf("[@me](mention://agent/%s) continue", agentID))

	if len(resp.TriggerOutcomes) != 1 {
		t.Fatalf("trigger_outcomes = %+v, want 1", resp.TriggerOutcomes)
	}
	o := resp.TriggerOutcomes[0]
	if o.TargetType != "agent" || o.TargetID != agentID {
		t.Fatalf("outcome target = %+v, want agent %s", o, agentID)
	}
	if o.Status != DispatchBlocked || o.ReasonCode != ReasonSelfTriggerSuppressed {
		t.Errorf("outcome = %+v, want blocked/self_trigger_suppressed (visible, not silent)", o)
	}
	if got := countSelfTriggerTasks(t, issueID, agentID); got != 1 {
		t.Errorf("agent tasks on issue = %d, want 1 (no new run enqueued)", got)
	}
}

// Acceptance #2 (end-to-end): the author's active run is on issue X, the
// self-mention lands on issue Y → the parent-handoff wake channel stays open
// (queued), the anti-loop must not break it.
func TestCreateComment_CrossIssueSelfWakeStillTriggers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, true)

	agentID := createHandlerTestAgent(t, "Cross-Issue Self-Wake Author", nil)
	sourceIssueID := createCommentTriggerPreviewIssue(t, "cross-issue wake source", "", "")
	targetIssueID := createCommentTriggerPreviewIssue(t, "cross-issue wake target", "", "")
	taskID := seedActiveTaskForSelfTrigger(t, agentID, sourceIssueID)

	resp := createSelfTriggerComment(t, targetIssueID, agentID, taskID,
		fmt.Sprintf("[@me](mention://agent/%s) handoff complete", agentID))

	var outcome *CommentTriggerOutcome
	for i := range resp.TriggerOutcomes {
		if resp.TriggerOutcomes[i].TargetID == agentID {
			outcome = &resp.TriggerOutcomes[i]
		}
	}
	if outcome == nil {
		t.Fatalf("trigger_outcomes = %+v, want an outcome for the self-mention target", resp.TriggerOutcomes)
	}
	if outcome.Status != DispatchQueued {
		t.Errorf("outcome = %+v, want queued (cross-issue self-wake must stay admitted)", outcome)
	}
	if got := countSelfTriggerTasks(t, targetIssueID, agentID); got != 1 {
		t.Errorf("agent tasks on target issue = %d, want 1 (wake enqueued)", got)
	}
}

// Acceptance #3: the composer preview and the save path share the same
// suppression — same input, same projection. The preview core
// (computeCommentAgentTriggers) must not promise an agent the save path
// (triggerTasksForComment) suppresses, and the blocked target surfaces in the
// preview's blocked list.
func TestCommentTriggerPreview_MatchesSavePathOnSelfMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, true)
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Preview-Save Parity Author", nil)
	issueID := createCommentTriggerPreviewIssue(t, "preview-save parity", "", "")
	seedActiveTaskForSelfTrigger(t, agentID, issueID)
	content := fmt.Sprintf("[@me](mention://agent/%s) continue", agentID)

	// Preview projection (member composer): the suppression is keyed on the
	// AUTHOR, and a member is never the author of an agent self-mention — the
	// agent stays in the preview agents list, matching what a member-authored
	// save would do (queued).
	preview := previewCommentTriggersForTest(t, issueID, map[string]any{"content": content})
	foundPreviewAgent := false
	for _, a := range preview.Agents {
		if a.ID == agentID {
			foundPreviewAgent = true
		}
	}
	if !foundPreviewAgent {
		t.Fatalf("preview agents = %+v, want the agent promised for a member author", preview.Agents)
	}

	// Agent-author parity: the compute core (shared by preview and save) and
	// the save path agree — the trigger is suppressed and the target marked.
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	triggers, targets := testHandler.computeCommentAgentTriggers(context.Background(), issue, content, nil, "agent", agentID, commentTriggerComputeOptions{})
	if len(triggers) != 0 {
		t.Fatalf("compute triggers = %+v, want the self trigger suppressed", triggers)
	}
	if len(targets) != 1 || targets[0].Status != DispatchBlocked || targets[0].ReasonCode != ReasonSelfTriggerSuppressed {
		t.Fatalf("compute targets = %+v, want blocked/self_trigger_suppressed", targets)
	}
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'agent', $3, $4)
		RETURNING id
	`, testWorkspaceID, issueID, agentID, content).Scan(&commentID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, commentID) })
	comment, err := testHandler.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          parseUUID(commentID),
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("load comment: %v", err)
	}
	outcomes := testHandler.triggerTasksForComment(ctx, issue, comment, nil, "agent", agentID, "", nil)
	if len(outcomes) != 1 || outcomes[0].Status != DispatchBlocked || outcomes[0].ReasonCode != ReasonSelfTriggerSuppressed {
		t.Fatalf("save outcomes = %+v, want blocked/self_trigger_suppressed (preview and save agree)", outcomes)
	}
}

// Acceptance #4: ordering with the RUYI-391 status gate. The status gate is
// the outer fence and gets the final word on non-admitted issues; on admitted
// issues (blocked status admits comment triggers) the suppression verdict
// stands.
func TestSelfTriggerSuppression_OrderingWithStatusGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, true)
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Gate-Ordering Author", nil)

	t.Run("non-admitted status: status gate reason wins", func(t *testing.T) {
		issueID := createCommentTriggerPreviewIssue(t, "gate ordering backlog", "", "")
		taskID := seedActiveTaskForSelfTrigger(t, agentID, issueID)
		if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'backlog' WHERE id = $1`, issueID); err != nil {
			t.Fatalf("set backlog: %v", err)
		}
		resp := createSelfTriggerComment(t, issueID, agentID, taskID,
			fmt.Sprintf("[@me](mention://agent/%s) continue", agentID))
		if len(resp.TriggerOutcomes) != 1 {
			t.Fatalf("trigger_outcomes = %+v, want 1", resp.TriggerOutcomes)
		}
		o := resp.TriggerOutcomes[0]
		if o.Status != DispatchBlocked || o.ReasonCode != ReasonIssueStatusNotDispatchable {
			t.Errorf("outcome = %+v, want blocked/issue_status_not_dispatchable (outer gate has the final word)", o)
		}
		if got := countSelfTriggerTasks(t, issueID, agentID); got != 1 {
			t.Errorf("agent tasks = %d, want 1 (status gate enqueues nothing)", got)
		}
	})

	t.Run("blocked status admits the comment and the suppression verdict stands", func(t *testing.T) {
		issueID := createCommentTriggerPreviewIssue(t, "gate ordering blocked status", "", "")
		taskID := seedActiveTaskForSelfTrigger(t, agentID, issueID)
		if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'blocked' WHERE id = $1`, issueID); err != nil {
			t.Fatalf("set blocked: %v", err)
		}
		resp := createSelfTriggerComment(t, issueID, agentID, taskID,
			fmt.Sprintf("[@me](mention://agent/%s) continue", agentID))
		if len(resp.TriggerOutcomes) != 1 {
			t.Fatalf("trigger_outcomes = %+v, want 1", resp.TriggerOutcomes)
		}
		o := resp.TriggerOutcomes[0]
		if o.Status != DispatchBlocked || o.ReasonCode != ReasonSelfTriggerSuppressed {
			t.Errorf("outcome = %+v, want blocked/self_trigger_suppressed (RUYI-391 admits blocked issues)", o)
		}
		if got := countSelfTriggerTasks(t, issueID, agentID); got != 1 {
			t.Errorf("agent tasks = %d, want 1", got)
		}
	})
}

// Acceptance #5 (end-to-end): with the switch off, a same-issue self mention
// behaves exactly as today — the agent is triggered (queued).
func TestCreateComment_SelfTriggerSwitchOffKeepsCurrentBehavior(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, false)

	agentID := createHandlerTestAgent(t, "Switch-Off Author", nil)
	issueID := createCommentTriggerPreviewIssue(t, "switch off", "", "")
	taskID := seedActiveTaskForSelfTrigger(t, agentID, issueID)

	resp := createSelfTriggerComment(t, issueID, agentID, taskID,
		fmt.Sprintf("[@me](mention://agent/%s) continue", agentID))

	var outcome *CommentTriggerOutcome
	for i := range resp.TriggerOutcomes {
		if resp.TriggerOutcomes[i].TargetID == agentID {
			outcome = &resp.TriggerOutcomes[i]
		}
	}
	if outcome == nil {
		t.Fatalf("trigger_outcomes = %+v, want an outcome for the self-mention target", resp.TriggerOutcomes)
	}
	if outcome.Status != DispatchQueued {
		t.Errorf("outcome = %+v, want queued (switch off = current behavior)", outcome)
	}
	if got := countSelfTriggerTasks(t, issueID, agentID); got != 2 {
		t.Errorf("agent tasks = %d, want 2 (seeded active + freshly enqueued)", got)
	}
}

// Coexistence: the existing squad-leader heuristic owns its verdict. A leader
// mid-run on the issue that @mentions its own squad hits the existing special
// case first (deferred/already_active — the active run covers the comment);
// the generic filter must not rewrite that outcome.
func TestCreateComment_LeaderSquadSelfMentionKeepsExistingVerdict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withSelfTriggerEnabled(t, true)

	leaderID := createHandlerTestAgent(t, "Leader Existing-Verdict Author", nil)
	squadID := createCommentTriggerPreviewSquad(t, "Leader Existing-Verdict Squad", leaderID)
	issueID := createCommentTriggerPreviewIssue(t, "leader existing verdict", "", "")
	taskID := seedActiveTaskForSelfTrigger(t, leaderID, issueID)

	resp := createSelfTriggerComment(t, issueID, leaderID, taskID,
		fmt.Sprintf("[@S](mention://squad/%s) revisit", squadID))

	if len(resp.TriggerOutcomes) != 1 {
		t.Fatalf("trigger_outcomes = %+v, want 1", resp.TriggerOutcomes)
	}
	o := resp.TriggerOutcomes[0]
	if o.Status != DispatchDeferred || o.ReasonCode != ReasonAlreadyActive {
		t.Errorf("outcome = %+v, want deferred/already_active from the existing heuristic, unmodified", o)
	}
	if got := countSelfTriggerTasks(t, issueID, leaderID); got != 1 {
		t.Errorf("leader tasks = %d, want 1 (no duplicate run)", got)
	}
}
