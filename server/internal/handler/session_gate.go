package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/agentconfig"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Session context gate, server side (RUYI-107).
//
// Everything here runs during a daemon claim, which is the only moment that has
// all three inputs at once: the candidate session, its measured size, and the
// owning agent's thresholds. Deciding on the daemon instead would need each
// host to query agent settings and usage rows, and would make the answer depend
// on daemon version — a gate whose behavior varies by host is not a gate.

const (
	// priorContextBriefMaxBytes bounds the whole assembled brief. It is a
	// budget, not an estimate of what fits: the brief exists to replace a
	// transcript that hit a 400K-token ceiling, so it must be small enough that
	// the fresh session it seeds is meaningfully emptier than the one it
	// replaced. ~32 KiB is roughly 8K tokens, i.e. ~2% of the default ceiling.
	priorContextBriefMaxBytes = 32 * 1024
	// priorContextBriefResultBytes bounds the previous run's own answer inside
	// that budget. It gets the largest single share because it is the only part
	// the issue record does NOT already contain in full.
	priorContextBriefResultBytes = 8 * 1024
	// priorContextBriefCommentBytes bounds each comment excerpt. Comments are
	// anchors, not content: the agent can and must read any of them in full
	// with `multica issue comment list`, so a longer excerpt buys nothing but
	// context it was trying to shed.
	priorContextBriefCommentBytes = 600
	// priorContextBriefThreadLimit / priorContextBriefRecentLimit bound how
	// many threads are listed at all.
	priorContextBriefThreadLimit = 40
	priorContextBriefUnresolved  = 8
	priorContextBriefRecent      = 8
)

// sessionGateOutcome is what applySessionContextGate decided, for logging.
type sessionGateOutcome struct {
	decision      agentconfig.SessionResumeDecision
	contextTokens int64
	known         bool
}

// contextSizeForTask reads the recorded context size of one task.
//
// A missing row, a NULL column, and a query error are all reported as unknown
// rather than as zero. That is deliberate and is the whole of decision D4 A: a
// gate that guesses "small" when it cannot measure would keep resuming an
// oversized session, and a gate that guessed "large" would throw away a healthy
// one on every transient database hiccup. Unknown resumes, and the pre-existing
// overflow protections stay responsible for the sessions this cannot see.
func (h *Handler) contextSizeForTask(ctx context.Context, taskID pgtype.UUID) (int64, bool) {
	if !taskID.Valid {
		return 0, false
	}
	tokens, err := h.Queries.GetTaskContextTokens(ctx, taskID)
	if err != nil || !tokens.Valid || tokens.Int64 <= 0 {
		return 0, false
	}
	return tokens.Int64, true
}

// decideSessionResumeForTask combines the agent's thresholds with the measured
// size of the task that owns the candidate session.
func (h *Handler) decideSessionResumeForTask(ctx context.Context, agent db.Agent, sourceTaskID pgtype.UUID) sessionGateOutcome {
	tokens, known := h.contextSizeForTask(ctx, sourceTaskID)
	return sessionGateOutcome{
		decision:      agentconfig.DecideSessionResume(tokens, known, agent.SessionMaxContextTokens, agent.SessionCompactPct),
		contextTokens: tokens,
		known:         known,
	}
}

// logSessionGate records the decision. Metadata only — no session text, no
// comment bodies, no agent output ever reaches the log, because the brief this
// gate produces is built from issue content that the log is not a store for.
func logSessionGate(taskID string, agentID string, outcome sessionGateOutcome, agent db.Agent, briefBytes int) {
	slog.Info("session context gate",
		"task_id", taskID,
		"agent_id", agentID,
		"decision", string(outcome.decision),
		"context_tokens", outcome.contextTokens,
		"context_tokens_known", outcome.known,
		"max_context_tokens", agent.SessionMaxContextTokens,
		"compact_pct", agent.SessionCompactPct,
		"brief_bytes", briefBytes,
	)
}

// applySessionContextGate is the single place a claim decides whether to keep
// resuming. Both resume paths (rerun and follow-up) route through it so they
// cannot drift apart; they differ only in which task they nominate as the
// session's source.
//
// On a compaction decision it drops PriorSessionID and attaches a brief in its
// place. It never touches PriorWorkDir: the working directory is not part of
// the conversation, and clearing it would turn a context handover into a lost
// worktree.
//
// Assembly failure degrades rather than blocks: the session is still abandoned
// (the whole point is that it is too big to resume), but the run is told its
// memory is gone through the existing continuity notice. Cancelling the
// compaction instead would resume the oversized session and lose the turn.
func (h *Handler) applySessionContextGate(ctx context.Context, resp *AgentTaskResponse, agent db.Agent, task db.AgentTaskQueue, sourceTaskID pgtype.UUID, priorResult []byte) {
	if resp.PriorSessionID == "" {
		// Nothing is being resumed, so there is nothing to gate. Skipping the
		// usage lookup here also keeps cold starts off the query.
		return
	}
	outcome := h.decideSessionResumeForTask(ctx, agent, sourceTaskID)
	if !outcome.decision.ShouldStartFreshSession() {
		logSessionGate(uuidToString(task.ID), uuidToString(task.AgentID), outcome, agent, 0)
		return
	}

	resp.PriorSessionID = ""
	brief := ""
	if task.IssueID.Valid {
		brief = h.buildPriorContextBrief(ctx, task.IssueID, parseUUID(resp.WorkspaceID), priorResult)
	}
	if brief == "" {
		resp.PriorSessionResumeUnavailable = true
	} else {
		resp.PriorContextBrief = brief
	}
	logSessionGate(uuidToString(task.ID), uuidToString(task.AgentID), outcome, agent, len(brief))
}

// priorRunResultText digs the previous run's own answer out of its stored
// result blob. CompleteTask marshals the whole TaskCompleteRequest into that
// JSONB column, so `output` is the field that holds what the agent actually
// said; anything else in there is bookkeeping.
func priorRunResultText(result []byte) string {
	if len(result) == 0 {
		return ""
	}
	var payload struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Output)
}

// clipForBrief truncates on a rune boundary and says so.
//
// Rune-aware because a byte-sliced multi-byte character would put invalid UTF-8
// into a prompt; the marker matters because an agent that cannot tell a short
// comment from a clipped one will treat the clipped one as complete.
func clipForBrief(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "… [truncated]"
}

// briefCommentLine renders one thread anchor.
func briefCommentLine(row db.ListRootCommentsForIssueRow, authorName string) string {
	author := authorName
	if author == "" {
		author = row.AuthorType
	}
	if author == "" {
		author = "unknown"
	}
	id := uuidToString(row.ID)
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("- `%s` (%s, %s, %d replies): %s\n",
		short,
		author,
		timestampToString(row.LastActivityAt),
		row.ReplyCount,
		clipForBrief(row.Content, priorContextBriefCommentBytes),
	)
}

// buildPriorContextBrief assembles the hand-off a fresh session starts from.
//
// It is mechanically assembled from data the platform already has — no model
// call, no summarisation, no external service (decision D1 A). That is a
// property worth keeping: the brief is built on the claim path, which every
// task blocks on, and it is built out of issue content whose confidentiality
// the platform is responsible for.
//
// Composition, and why each part is here:
//   - the previous run's own answer, because it is the one thing the issue
//     record does not already hold in full;
//   - unresolved threads, because they are the open work;
//   - the most recently active threads, because they are what the conversation
//     is currently about.
//
// Everything after the first part is an ANCHOR — id, author, timestamp, excerpt
// — not the content itself. The agent has `multica issue comment list` and its
// workflow already requires reading the issue; re-injecting full comment bodies
// would rebuild the context that just got too large.
//
// Returns "" when there is nothing worth handing over, which the caller treats
// as a failed assembly and degrades accordingly.
func (h *Handler) buildPriorContextBrief(ctx context.Context, issueID, workspaceID pgtype.UUID, priorResult []byte) string {
	var b strings.Builder
	b.WriteString("## Prior Session Context\n\n")
	b.WriteString("Your earlier session on this issue grew close to its context limit, so this run starts on a fresh one. " +
		"The issue and its full comment history are unaffected and remain the authoritative record — what follows is a mechanical hand-off, not a summary, and not a substitute for reading the issue. " +
		"Your own working memory from the earlier turns is gone: re-derive what you need rather than assuming it, and do not open your reply by announcing this.\n\n")

	if result := priorRunResultText(priorResult); result != "" {
		b.WriteString("### What your previous run reported\n\n")
		b.WriteString(clipForBrief(result, priorContextBriefResultBytes))
		b.WriteString("\n\n")
	}

	roots, err := h.Queries.ListRootCommentsForIssue(ctx, db.ListRootCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: workspaceID,
		RowLimit:    priorContextBriefThreadLimit,
	})
	if err != nil {
		slog.Warn("session context gate: load issue threads for brief failed",
			"issue_id", uuidToString(issueID), "error", err)
		roots = nil
	}

	// One name lookup per distinct author rather than per row: a busy issue is
	// mostly a handful of participants, and the claim path is latency-visible.
	authorNames := make(map[string]string)
	nameFor := func(row db.ListRootCommentsForIssueRow) string {
		if !row.AuthorID.Valid {
			return ""
		}
		key := row.AuthorType + ":" + uuidToString(row.AuthorID)
		if name, ok := authorNames[key]; ok {
			return name
		}
		name := ""
		switch row.AuthorType {
		case "agent":
			if a, err := h.Queries.GetAgent(ctx, row.AuthorID); err == nil {
				name = a.Name
			}
		case "member":
			if u, err := h.Queries.GetUser(ctx, row.AuthorID); err == nil {
				name = u.Name
			}
		}
		authorNames[key] = name
		return name
	}

	var unresolved []db.ListRootCommentsForIssueRow
	for _, row := range roots {
		if !row.ResolvedAt.Valid {
			unresolved = append(unresolved, row)
		}
	}
	// Newest-first within each section: on an issue with more open threads than
	// the budget allows, the ones still being discussed are the ones worth
	// keeping.
	sort.SliceStable(unresolved, func(i, j int) bool {
		return unresolved[i].LastActivityAt.Time.After(unresolved[j].LastActivityAt.Time)
	})
	if len(unresolved) > priorContextBriefUnresolved {
		unresolved = unresolved[:priorContextBriefUnresolved]
	}
	if len(unresolved) > 0 {
		b.WriteString("### Unresolved threads\n\n")
		for _, row := range unresolved {
			b.WriteString(briefCommentLine(row, nameFor(row)))
		}
		b.WriteString("\n")
	}

	recent := make([]db.ListRootCommentsForIssueRow, len(roots))
	copy(recent, roots)
	sort.SliceStable(recent, func(i, j int) bool {
		return recent[i].LastActivityAt.Time.After(recent[j].LastActivityAt.Time)
	})
	if len(recent) > priorContextBriefRecent {
		recent = recent[:priorContextBriefRecent]
	}
	if len(recent) > 0 {
		b.WriteString("### Most recent activity\n\n")
		for _, row := range recent {
			b.WriteString(briefCommentLine(row, nameFor(row)))
		}
		b.WriteString("\n")
	}

	// Nothing but the boilerplate header means we have no hand-off to give.
	// Reporting that honestly lets the caller fall back to the plain continuity
	// notice, which at least tells the agent its memory is gone.
	if len(unresolved) == 0 && len(recent) == 0 && priorRunResultText(priorResult) == "" {
		return ""
	}

	b.WriteString("Read the issue and the threads above with `multica issue comment list` before acting on any of this.\n\n")

	return clipForBrief(b.String(), priorContextBriefMaxBytes)
}
