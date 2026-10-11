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
	"log/slog"
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
// everything else — a malformed report fails the run visibly. The error is
// internal (it can quote the raw output); callers log it server-side and
// record the stable reason code on the run row, never the error text.
func ParseRunOutput(output string) (RunOutput, error) {
	var out RunOutput
	if err := json.Unmarshal([]byte(stripJSONFences(output)), &out); err != nil {
		return RunOutput{}, fmt.Errorf("retrospective: agent report is not valid JSON: %w", err)
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
// trigger (the membership set the agent's report is attributed against) plus
// outcome fields merged in at finish. Reason is the structured failure
// verdict (RUYI-561): the UI localizes from its code; nil on success.
// RUYI-674 membership breadcrumbs: ids the report named outside the scanned
// set either got auto-corrected onto a unique near neighbor, were accepted
// as real workspace issues (drafts land, no watermark), or were dropped as
// unattributable — each category lands in its own list, length = count.
type runDetail struct {
	IssueIDs              []string       `json:"issue_ids,omitempty"`
	AnalyzedIssueIDs      []string       `json:"analyzed_issue_ids,omitempty"`
	CorrectedIssueIDs     []idCorrection `json:"corrected_issue_ids,omitempty"`
	IgnoredIssueIDs       []string       `json:"ignored_issue_ids,omitempty"`
	AcceptedOutOfScopeIDs []string       `json:"accepted_out_of_scope_ids,omitempty"`
	Reason                *runReason     `json:"reason,omitempty"`
}

// idCorrection records one reported id auto-corrected onto the scanned set
// (the f15f→f15a class of single-character copy typos).
type idCorrection struct {
	From string `json:"from"`
	To   string `json:"to"`
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

	if strings.TrimSpace(taskErr) != "" || strings.TrimSpace(output) == "" {
		var reason *runReason
		if strings.TrimSpace(taskErr) != "" {
			// The daemon's failure text is internal (error chains, paths);
			// the server log keeps it verbatim, the run row gets only the
			// stable code (RUYI-561).
			slog.Warn("retrospective: agent run failed",
				"run_id", tc.RunID, "task_id", util.UUIDToString(task.ID), "error", taskErr)
			reason = &runReason{Code: ReasonAgentRunFailed}
		} else {
			reason = &runReason{Code: ReasonAgentNoReport}
		}
		return stats, r.finishRun(ctx, run, "failed", reason, runDetail{IssueIDs: detail.IssueIDs, AnalyzedIssueIDs: nil}, stats)
	}

	out, err := ParseRunOutput(output)
	if err != nil {
		// The parse error can quote raw agent output — log material, not UI
		// material (RUYI-561).
		slog.Warn("retrospective: agent report rejected",
			"run_id", tc.RunID, "task_id", util.UUIDToString(task.ID), "error", err)
		return stats, r.finishRun(ctx, run, "failed", &runReason{Code: ReasonAgentReportInvalid}, runDetail{IssueIDs: detail.IssueIDs}, stats)
	}
	// Membership is a filter, not a gate (RUYI-674): every id the report
	// names is attributed instead of the report being rejected wholesale.
	// A unique single-edit neighbor is auto-corrected (the f15f→f15a class
	// of copy typos), ids naming a real issue of this workspace keep their
	// drafts (no watermark), and only unattributable ids are dropped — each
	// category with a breadcrumb in the run detail. Nothing on this path
	// fails the run; hard failures stay parse/runtime errors above.
	analyzed := dedupeStrings(out.AnalyzedIssueIDs)
	for i := range out.Drafts {
		out.Drafts[i].IssueID = strings.TrimSpace(out.Drafts[i].IssueID)
	}
	reported := dedupeStrings(append(append([]string{}, analyzed...), draftIssueIDs(out.Drafts)...))
	kinds, resolvedByID, corrections, err := r.classifyReportedIDs(ctx, tc.WorkspaceID, reported, detail.IssueIDs)
	if err != nil {
		return stats, err
	}

	var analyzedOut, ignoredIDs, foreignIDs []string
	for _, id := range analyzed {
		switch kinds[id] {
		case idInScope:
			analyzedOut = append(analyzedOut, resolvedByID[id])
		case idOutOfScopeReal:
			analyzedOut = append(analyzedOut, id)
			foreignIDs = append(foreignIDs, id)
		default: // idUnattributable
			ignoredIDs = append(ignoredIDs, id)
		}
	}
	analyzedOut = dedupeStrings(analyzedOut)

	// Watermark eligibility by attributed id: only the scanned set (exact or
	// corrected) is ever watermarked (RUYI-674 requirement 4).
	watermarkOK := make(map[string]bool, len(reported))
	for _, id := range reported {
		switch kinds[id] {
		case idInScope:
			watermarkOK[resolvedByID[id]] = true
		case idOutOfScopeReal:
			watermarkOK[id] = false
		}
	}

	// Drafts whose issue id is unattributable are dropped with the id; the
	// rest keep their (possibly corrected) attribution.
	keptDrafts := make([]extractedDraft, 0, len(out.Drafts))
	for _, d := range out.Drafts {
		switch kinds[d.IssueID] {
		case idInScope:
			d.IssueID = resolvedByID[d.IssueID]
			keptDrafts = append(keptDrafts, d)
		case idOutOfScopeReal:
			keptDrafts = append(keptDrafts, d)
		default: // idUnattributable — its draft is discarded with it
		}
	}

	// Breadcrumbs: run detail carries the id lists, the server log carries
	// the same accounting (warn for the dropped ids). IDs and counts only —
	// never report text (RUYI-561 discipline).
	stats.IDsCorrected = len(corrections)
	for _, c := range corrections {
		slog.Info("retrospective: reported issue id auto-corrected onto the scanned set",
			"run_id", tc.RunID, "task_id", util.UUIDToString(task.ID), "from", c.From, "to", c.To)
	}
	stats.IDsIgnored = len(ignoredIDs)
	if len(ignoredIDs) > 0 {
		slog.Warn("retrospective: unattributable issue ids ignored",
			"run_id", tc.RunID, "task_id", util.UUIDToString(task.ID),
			"count", len(ignoredIDs), "issue_ids", strings.Join(ignoredIDs, ","))
	}
	if len(foreignIDs) > 0 {
		slog.Info("retrospective: out-of-scope issue accepted without watermark",
			"run_id", tc.RunID, "task_id", util.UUIDToString(task.ID),
			"issue_ids", strings.Join(foreignIDs, ","))
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
	for _, d := range keptDrafts {
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

	// Every attributed id with results to land — the analyzed ids plus any
	// issue the agent drafted for without listing it under analyzed_issue_ids
	// (the output contract allows that shape; its work must not vanish).
	processIDs := dedupeStrings(append(append([]string{}, analyzedOut...), draftIssueIDs(keptDrafts)...))
	for _, issueID := range processIDs {
		drafts := draftsByIssue[issueID]
		watermarkEligible := watermarkOK[issueID]
		if len(drafts) == 0 {
			// Analyzed, nothing worth proposing — watermark so the issue is
			// never re-read (only ever an in-scope id).
			stats.IssuesAnalyzed++
			if watermarkEligible {
				if err := r.watermark(ctx, tc.WorkspaceID, issueID, tc.RunID); err != nil {
					return stats, err
				}
			}
			continue
		}
		for _, d := range drafts {
			if err := r.upsertDraft(ctx, tc.WorkspaceID, tc.RunID, issueID, d, liveClauses, stats, watermarkEligible); err != nil {
				return stats, err
			}
		}
		stats.IssuesAnalyzed++
	}

	return stats, r.finishRun(ctx, run, "succeeded", nil, runDetail{
		IssueIDs:              detail.IssueIDs,
		AnalyzedIssueIDs:      analyzedOut,
		CorrectedIssueIDs:     corrections,
		IgnoredIssueIDs:       ignoredIDs,
		AcceptedOutOfScopeIDs: foreignIDs,
	}, stats)
}

// classifyReportedIDs decides where every distinct issue id named by the
// report belongs (RUYI-674). In-scope ids — exact hits or a unique
// single-edit neighbor corrected onto the scanned set — map through
// resolvedByID; ids naming a real issue of this workspace are accepted
// out-of-scope (their drafts land, no watermark); everything else is
// unattributable and gets dropped with a breadcrumb.
func (r *Runner) classifyReportedIDs(ctx context.Context, workspaceID string, reported, scannedIDs []string) (map[string]reportedIDKind, map[string]string, []idCorrection, error) {
	kinds := make(map[string]reportedIDKind, len(reported))
	resolvedByID := make(map[string]string, len(reported))
	var corrections []idCorrection
	for _, id := range reported {
		if target, corrected, ok := resolveIssueID(id, scannedIDs); ok {
			kinds[id] = idInScope
			resolvedByID[id] = target
			if corrected {
				corrections = append(corrections, idCorrection{From: id, To: target})
			}
			continue
		}
		var kind reportedIDKind
		if parsed, parseErr := util.ParseUUID(id); parseErr == nil {
			exists, probeErr := r.Queries.RetrospectiveIssueExistsInWorkspace(ctx, db.RetrospectiveIssueExistsInWorkspaceParams{
				WorkspaceID: util.MustParseUUID(workspaceID),
				IssueID:     parsed,
			})
			if probeErr != nil {
				return nil, nil, nil, fmt.Errorf("retrospective: 探测越界 Issue 归属: %w", probeErr)
			}
			if exists {
				kind = idOutOfScopeReal
				resolvedByID[id] = id
			}
		}
		kinds[id] = kind
	}
	return kinds, resolvedByID, corrections, nil
}

// upsertDraft is the two-layer dedup for one draft, one transaction: pool
// merge beats a duplicate row; the watermark rides along so a crash cannot
// re-analyze the issue. watermark is false for ids attributed outside the
// scanned set (RUYI-674): their drafts land but the next pass may rescan.
func (r *Runner) upsertDraft(ctx context.Context, workspaceID, runID, issueID string, d extractedDraft, liveClauses map[string]bool, stats *RunStats, watermark bool) error {
	if d.ChangeKind == "add_clause" && liveClauses[strings.ToLower(d.ClauseName)] {
		// 现行条款重复预检: the clause already exists in the carrier.
		stats.DuplicatesSkipped++
		if watermark {
			return r.watermark(ctx, workspaceID, issueID, runID)
		}
		return nil
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
			if watermark {
				return q.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
					WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
				})
			}
			return nil
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
		if watermark {
			return q.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
				WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
			})
		}
		return nil
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

// reportedIDKind is what the membership filter decided about one id the
// agent's report named (RUYI-674).
type reportedIDKind uint8

const (
	// idUnattributable: not correctable onto the scanned set and not a real
	// issue of this workspace — dropped, with a breadcrumb in the run detail.
	idUnattributable reportedIDKind = iota
	// idInScope: names (exactly, or after single-edit correction) an issue in
	// this run's scanned set — fully attributed, watermark eligible.
	idInScope
	// idOutOfScopeReal: outside the scanned set but a real issue of this
	// workspace — its drafts land (anchors carry the reported id), no
	// watermark, the next pass may rescan it.
	idOutOfScopeReal
)

// editDistanceAtMost1 reports whether a and b differ by at most one
// single-character edit (insert, delete, or substitute). Issue ids are
// ASCII UUIDs, but rune-wise comparison keeps it correct for any noise.
func editDistanceAtMost1(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	if len(rb)-len(ra) > 1 {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			if len(ra) == len(rb) {
				// Substitution: the tails after the differing rune must
				// match exactly for one edit to suffice.
				return string(ra[i+1:]) == string(rb[i+1:])
			}
			// One insertion into ra (deletion from rb): the rest of ra must
			// equal rb shifted by one.
			return string(ra[i:]) == string(rb[i+1:])
		}
	}
	return true // ra is a prefix of rb; the distance is the length gap ≤ 1
}

// resolveIssueID attributes one reported id against the scanned set: an
// exact hit returns itself; a unique single-edit neighbor is returned for
// auto-correction (the f15f→f15a class of copy typos, RUYI-674). Two or
// more equidistant neighbors — or none — leave the id unresolved.
func resolveIssueID(reported string, scannedIDs []string) (resolved string, corrected bool, ok bool) {
	for _, id := range scannedIDs {
		if id == reported {
			return id, false, true
		}
	}
	for _, id := range scannedIDs {
		if editDistanceAtMost1(reported, id) {
			if corrected {
				// A second equidistant candidate: attribution would be a
				// coin flip, treat the id as out of scope.
				return "", false, false
			}
			resolved, corrected = id, true
		}
	}
	return resolved, corrected, corrected
}

// draftIssueIDs lists the issue ids the report's drafts reference, in order.
func draftIssueIDs(drafts []extractedDraft) []string {
	out := make([]string, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, d.IssueID)
	}
	return out
}
