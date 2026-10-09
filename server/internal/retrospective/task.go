package retrospective

// The agent-run side of the daily retrospective (RUYI-552 direction 3): the
// payload the enqueued platform task carries, the prompt the claim path
// renders from it, and the parsing/validation of the agent's JSON report.
//
// The task is a no-issue quiz-style run (originator_source='retrospective',
// issue_id NULL): the agent reads the window's completed issues with its own
// tools and reports improvement drafts as one JSON object in its final
// message. The server never assembles issue content for the model, so the
// promptscan step of the direct-LLM design disappears with it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// TaskOriginatorSource tags the enqueued task's originator_source column,
	// the same discipline as the prompt-quiz run: a no-issue run with its own
	// source label stays out of the quick_create production statistic and out
	// of every issue-dimension query (those all JOIN issue).
	TaskOriginatorSource = "retrospective"
	// TaskKind is the activity-UI discriminator computeTaskKind returns for
	// these runs.
	TaskKind = "retrospective"
	// TriggerEvidenceKind pairs with the retrospective_run id in the task's
	// trigger_evidence_ref_id — the audit pointer from the task back to the
	// run record that ordered it.
	TriggerEvidenceKind = "retrospective_run"
	// MaxDraftsPerIssue caps the agent's per-issue output; the watermark and
	// the next run recover the remainder.
	MaxDraftsPerIssue = 3
	// maxClauseNameRunes keeps clause names directly enactable.
	maxClauseNameRunes = 60
	// maxTitleRunes bounds one issue title inside the context payload.
	maxTitleRunes = 200
)

// TaskContextIssue is one scanned issue inside the task payload — id plus
// title only. The agent fetches the real content itself.
type TaskContextIssue struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// TaskContext is the JSONB payload the enqueued task carries: everything the
// claim path needs to render the run's prompt, and nothing else — there is
// no issue for this run, by design.
type TaskContext struct {
	Kind        string             `json:"kind"`
	RunID       string             `json:"run_id"`
	WorkspaceID string             `json:"workspace_id"`
	WindowStart string             `json:"window_start"` // RFC3339
	WindowEnd   string             `json:"window_end"`   // RFC3339
	Issues      []TaskContextIssue `json:"issues"`
}

// ParseTaskContext reports whether the raw context JSONB is a retrospective
// payload (kind + valid uuids) and returns it. Tasks linked to an issue,
// chat or autopilot are never retrospective runs even if they carry a
// context blob; every other shape returns false so callers short-circuit.
func ParseTaskContext(raw []byte) (TaskContext, bool) {
	var tc TaskContext
	if len(raw) == 0 || json.Unmarshal(raw, &tc) != nil {
		return TaskContext{}, false
	}
	if tc.Kind != TaskKind {
		return TaskContext{}, false
	}
	if _, err := util.ParseUUID(tc.RunID); err != nil {
		return TaskContext{}, false
	}
	if _, err := util.ParseUUID(tc.WorkspaceID); err != nil {
		return TaskContext{}, false
	}
	return tc, true
}

// PromptFromContext renders the run's full prompt from the raw context
// JSONB. This is the claim path's entire assignment surface: boundaries
// first (no issue may be created or commented), then the output contract —
// the server parses the final message, so its shape is load-bearing.
func PromptFromContext(raw []byte) (string, bool) {
	tc, ok := ParseTaskContext(raw)
	if !ok || len(tc.Issues) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	b.WriteString("This run is the workspace's daily retrospective. It was triggered automatically by the retrospective schedule (or manually by a workspace owner). Your entire assignment: review the completed issues listed below and distill Prompt-improvement drafts from them, reported as one JSON object in your FINAL message. The server parses that message — it is the only thing recorded.\n\n")
	b.WriteString("Hard boundaries:\n")
	b.WriteString("- Do NOT create, update, or comment on any issue. There is no assigned issue for this run and none may be created; do not run `multica issue create` and do not post comments.\n")
	b.WriteString("- Your final text message must be exactly one JSON object (no markdown fences, no prose around it) matching the contract below.\n\n")
	fmt.Fprintf(&b, "Workspace ID: %s\n", tc.WorkspaceID)
	fmt.Fprintf(&b, "Retrospective run ID: %s\n", tc.RunID)
	fmt.Fprintf(&b, "Review window: %s to %s (issues last updated inside)\n\n", tc.WindowStart, tc.WindowEnd)
	b.WriteString("Issues in scope — read each with `multica issue get <id> --output json`, and scan its comment threads with `multica issue comment list <id> --roots-only --summary --compact --output json` (expand only what matters):\n")
	for _, issue := range tc.Issues {
		fmt.Fprintf(&b, "- %s — %s\n", issue.ID, issue.Title)
	}
	b.WriteString("\nFor each issue worth learning from, propose at most 3 drafts. A draft must be written as final, directly-enactable clause text (no revision marks, no weak wording), carry the five content-gate answers (layer / retention / cost / conflict / dedup) and cite its evidence. If an issue yields nothing worth proposing, still list its id in analyzed_issue_ids so it is not re-read by the next run.\n\n")
	b.WriteString(`Output contract (the server rejects output it cannot parse, and the run then fails visibly):
{"analyzed_issue_ids":["<issue id>"],"drafts":[{"issue_id":"<issue id>","carrier_scope":"workspace","target_section":"section name or empty string","change_kind":"add_clause|revise_clause|remove_clause","clause_name":"clause name","clause_text":"- **clause name**: clause body","gate_answers":{"layer":"...","retention":"...","cost":"...","conflict":"...","dedup":"..."},"evidence_ref":"issue title or comment summary","evidence_note":"one-sentence justification"}]}

`)
	b.WriteString("Rules: every issue id (in analyzed_issue_ids and in each draft) must be one of the ids listed above; clause_name is at most 60 characters; a remove_clause draft may leave clause_text empty. Duplicates and already-covered clauses are deduplicated server-side — do not spend effort re-checking the current prompt content.\n")
	return b.String(), true
}

// RunOutput is one parsed agent report.
type RunOutput struct {
	AnalyzedIssueIDs []string         `json:"analyzed_issue_ids"`
	Drafts           []extractedDraft `json:"drafts"`
}

// extractedDraft mirrors one draft in the agent's JSON report.
type extractedDraft struct {
	IssueID       string `json:"issue_id"`
	CarrierScope  string `json:"carrier_scope"`
	TargetSection string `json:"target_section"`
	ChangeKind    string `json:"change_kind"`
	ClauseName    string `json:"clause_name"`
	ClauseText    string `json:"clause_text"`
	GateAnswers   struct {
		Layer     string `json:"layer"`
		Retention string `json:"retention"`
		Cost      string `json:"cost"`
		Conflict  string `json:"conflict"`
		Dedup     string `json:"dedup"`
	} `json:"gate_answers"`
	EvidenceRef  string `json:"evidence_ref"`
	EvidenceNote string `json:"evidence_note"`
}

// ParseRunOutput parses the agent's final message into a report. Tolerant of
// markdown fences (models add them despite instructions); intolerant of
// everything else — a malformed report fails the run visibly.
func ParseRunOutput(output string) (RunOutput, error) {
	var out RunOutput
	if err := json.Unmarshal([]byte(stripJSONFences(output)), &out); err != nil {
		return RunOutput{}, fmt.Errorf("Agent 输出不是合法的复盘 JSON: %w", err)
	}
	return out, nil
}

// stripJSONFences removes a wrapping ```json …``` block if present.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// runDetail is the JSONB the run row carries: the scanned issue ids from the
// trigger (the membership set the agent's report is validated against) plus
// outcome fields merged in at finish.
type runDetail struct {
	IssueIDs         []string `json:"issue_ids,omitempty"`
	AnalyzedIssueIDs []string `json:"analyzed_issue_ids,omitempty"`
}

func parseRunDetail(raw []byte) runDetail {
	var d runDetail
	_ = json.Unmarshal(raw, &d)
	return d
}

// ProcessTaskTerminal is the completion-side entry the TaskService calls
// when a retrospective task reaches a terminal state. output is the agent's
// final message on the completed path (empty on failure paths); taskErr is
// the task's error text. The run row is the only written surface — drafts
// into the proposal pool, watermarks, counts — never an issue.
//
// The first terminal verdict wins: a run the bulk reconciler already failed
// (task cancelled offline, etc.) is left alone.
func (r *Runner) ProcessTaskTerminal(ctx context.Context, task db.AgentTaskQueue, output, taskErr string) (*RunStats, error) {
	tc, ok := ParseTaskContext(task.Context)
	if !ok {
		return nil, fmt.Errorf("retrospective: task %s carries no retrospective context", util.UUIDToString(task.ID))
	}
	run, err := r.Queries.GetRetrospectiveRunByTaskID(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("retrospective: run for task %s: %w", util.UUIDToString(task.ID), err)
	}
	if run.Status != "running" {
		return nil, nil
	}

	stats := &RunStats{WorkspaceID: tc.WorkspaceID, RunID: tc.RunID}
	detail := parseRunDetail(run.Detail)
	scanned := make(map[string]bool, len(detail.IssueIDs))
	for _, id := range detail.IssueIDs {
		scanned[id] = true
	}

	if strings.TrimSpace(taskErr) != "" || strings.TrimSpace(output) == "" {
		msg := taskErr
		if strings.TrimSpace(msg) == "" {
			msg = "智能体运行结束但未产出复盘结果"
		}
		return stats, r.finishRun(ctx, run, "failed", msg, runDetail{IssueIDs: detail.IssueIDs, AnalyzedIssueIDs: nil}, stats)
	}

	out, err := ParseRunOutput(output)
	if err != nil {
		return stats, r.finishRun(ctx, run, "failed", err.Error(), runDetail{IssueIDs: detail.IssueIDs}, stats)
	}
	// All-or-nothing membership validation before any write: a report that
	// names issues outside this run's scanned set is rejected wholesale, so a
	// confused agent can never watermark or draft against foreign issues.
	analyzed := dedupeStrings(out.AnalyzedIssueIDs)
	for _, id := range analyzed {
		if !scanned[id] {
			return stats, r.finishRun(ctx, run, "failed",
				fmt.Sprintf("Agent 报告了本轮回看窗口之外的 Issue（%s），整份报告已拒绝", id),
				runDetail{IssueIDs: detail.IssueIDs}, stats)
		}
	}
	for _, d := range out.Drafts {
		if !scanned[d.IssueID] {
			return stats, r.finishRun(ctx, run, "failed",
				fmt.Sprintf("Agent 草案引用了本轮回看窗口之外的 Issue（%s），整份报告已拒绝", d.IssueID),
				runDetail{IssueIDs: detail.IssueIDs}, stats)
		}
	}

	// Current-clause pre-check source: the workspace carrier's effective
	// content, loaded once per report.
	current, err := r.Queries.GetWorkspacePromptContent(ctx, run.WorkspaceID)
	if err != nil {
		return stats, fmt.Errorf("retrospective: 读取现行载体内容: %w", err)
	}
	liveClauses := map[string]bool{}
	for _, c := range legislation.Clauses(current) {
		liveClauses[strings.ToLower(c)] = true
	}

	// Normalize drafts into per-issue buckets: unknown change kinds and
	// nameless/oversized clauses are dropped here (counted, not fatal), the
	// same accounting the direct-LLM design used.
	draftsByIssue := map[string][]extractedDraft{}
	for _, d := range out.Drafts {
		switch d.ChangeKind {
		case "add_clause", "revise_clause", "remove_clause":
		default:
			stats.DuplicatesSkipped++
			continue
		}
		if d.ClauseName == "" || len([]rune(d.ClauseName)) > maxClauseNameRunes {
			stats.DuplicatesSkipped++
			continue
		}
		if d.ChangeKind != "remove_clause" && strings.TrimSpace(d.ClauseText) == "" {
			stats.DuplicatesSkipped++
			continue
		}
		if len(draftsByIssue[d.IssueID]) >= MaxDraftsPerIssue {
			continue
		}
		draftsByIssue[d.IssueID] = append(draftsByIssue[d.IssueID], d)
	}

	for _, issueID := range analyzed {
		drafts := draftsByIssue[issueID]
		if len(drafts) == 0 {
			// Analyzed, nothing worth proposing — watermark so the issue is
			// never re-read.
			stats.IssuesAnalyzed++
			if err := r.watermark(ctx, tc.WorkspaceID, issueID, tc.RunID); err != nil {
				return stats, err
			}
			continue
		}
		for _, d := range drafts {
			if err := r.upsertDraft(ctx, tc.WorkspaceID, tc.RunID, issueID, d, liveClauses, stats); err != nil {
				return stats, err
			}
		}
		stats.IssuesAnalyzed++
	}

	return stats, r.finishRun(ctx, run, "succeeded", "", runDetail{IssueIDs: detail.IssueIDs, AnalyzedIssueIDs: analyzed}, stats)
}

// upsertDraft is the two-layer dedup for one draft, one transaction: pool
// merge beats a duplicate row; the watermark rides along so a crash cannot
// re-analyze the issue.
func (r *Runner) upsertDraft(ctx context.Context, workspaceID, runID, issueID string, d extractedDraft, liveClauses map[string]bool, stats *RunStats) error {
	if d.ChangeKind == "add_clause" && liveClauses[strings.ToLower(d.ClauseName)] {
		// 现行条款重复预检: the clause already exists in the carrier.
		stats.DuplicatesSkipped++
		return r.watermark(ctx, workspaceID, issueID, runID)
	}

	anchors, _ := json.Marshal([]map[string]string{{
		"issue_id": issueID, "ref": d.EvidenceRef, "note": truncateRunes(d.EvidenceNote, 200),
	}})
	audit, _ := json.Marshal([]map[string]any{{
		"action": "created", "actor_type": "system", "actor_id": runID,
		"at": r.now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}})

	return r.withTx(ctx, func(q *db.Queries) error {
		existing, err := q.FindMergablePromptProposal(ctx, db.FindMergablePromptProposalParams{
			WorkspaceID:    util.MustParseUUID(workspaceID),
			CarrierScope:   "workspace",
			CarrierScopeID: util.MustParseUUID(workspaceID),
			ChangeKind:     d.ChangeKind,
			ClauseName:     d.ClauseName,
		})
		if err == nil {
			cand, _ := json.Marshal([]map[string]string{{"issue_id": issueID, "run_id": runID}})
			if _, err := q.MergePromptProposalEvidence(ctx, db.MergePromptProposalEvidenceParams{
				ID:         existing.ID,
				Anchors:    anchors,
				MergedFrom: cand,
				Audit:      audit,
			}); err != nil {
				return err
			}
			stats.ProposalsMerged++
			return q.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
				WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
			})
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := q.CreatePromptProposal(ctx, db.CreatePromptProposalParams{
			WorkspaceID:         util.MustParseUUID(workspaceID),
			CarrierScope:        "workspace",
			CarrierScopeID:      util.MustParseUUID(workspaceID),
			TargetSection:       d.TargetSection,
			ChangeKind:          d.ChangeKind,
			ClauseName:          d.ClauseName,
			ClauseText:          d.ClauseText,
			GateAnswerLayer:     d.GateAnswers.Layer,
			GateAnswerRetention: d.GateAnswers.Retention,
			GateAnswerCost:      d.GateAnswers.Cost,
			GateAnswerConflict:  d.GateAnswers.Conflict,
			GateAnswerDedup:     d.GateAnswers.Dedup,
			EvidenceAnchors:     anchors,
			Source:              "retrospective",
			CreatedByType:       "system",
			CreatedByID:         util.MustParseUUID(workspaceID),
			AuditLog:            audit,
		}); err != nil {
			return err
		}
		stats.ProposalsCreated++
		return q.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
			WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
		})
	})
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
