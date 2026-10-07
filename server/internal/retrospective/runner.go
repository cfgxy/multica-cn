// Package retrospective is the daily retrospective (RUYI-305 E3): a
// base-layer scheduled task that analyzes the real execution content of
// issues completed inside the configured window and distills Prompt
// improvement drafts into the proposal pool.
//
// Hard boundaries (dry-run patches 1–4, 9–10):
//
//   - It never writes to issues — no comments, no reports, no
//     notifications. Every outcome lands in prompt_proposal (drafts) and
//     retrospective_run (records). Failures are visible only on the
//     self-evolution retrospective page.
//   - It reads the issue's real execution content — description, comments,
//     progress updates — never a pre-generated retrospective comment (B
//     语义修正). The package itself writes nothing an issue could echo back.
//   - Idempotency is per-issue (retrospective_issue_watermark): an issue
//     analyzed by any successful run is never analyzed again, so overlapping
//     windows cannot produce duplicate drafts.
//   - Draft dedup is two-layer: pool merge against a live same-topic draft
//     (evidence anchors accumulate), and a current-clause pre-check against
//     the workspace carrier's effective content (an add_clause whose clause
//     name already exists is skipped, not re-proposed).
//
// The LLM is the single sanctioned entry point (pkg/llm). When the
// deployment has no LLM configured, the run records a failed run with that
// reason — the page shows it; nothing else in the product notices.
package retrospective

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptscan"
)

// Per-run bounds. IssuesPerRun caps model spend per tick (the watermark and
// next tick recover the remainder); the input budget keeps one noisy issue
// from blowing the model context.
const (
	IssuesPerRun       = 20
	MaxInputChars      = 12000
	MaxCommentChars    = 800
	MaxDraftsPerIssue  = 3
	LLMTimeout         = 2 * time.Minute
	maxClauseNameRunes = 60
)

// LLMClient is the slice of pkg/llm the retrospective consumes. An
// interface keeps this package (and its tests) free of the concrete client.
type LLMClient interface {
	GenerateJSON(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64, maxCompletionTokens int64) (string, error)
	Enabled() bool
}

// Runner executes retrospective runs for one workspace at a time. The
// scheduler drives RunWorkspace through a global-scope daily job; the
// handler's manual trigger path calls the same method with trigger=manual.
type Runner struct {
	// DB begins per-issue transactions (watermark + draft insert commit
	// together). Satisfied by *pgxpool.Pool and the handler's txStarter.
	DB interface {
		Begin(ctx context.Context) (pgx.Tx, error)
	}
	Queries *db.Queries
	LLM     LLMClient
	Model   string
	// Redact carries the resolved LLM API key (RUYI-552): every error string
	// landing in the run record passes through it, so an upstream failure
	// that echoes the key back still stores nothing sensitive.
	Redact []string
	// Now is overridable for tests.
	Now func() time.Time
}

// RunStats is one workspace pass's outcome, feeding the run record and the
// scheduler's HandlerResult.
type RunStats struct {
	WorkspaceID       string `json:"workspace_id"`
	RunID             string `json:"run_id"`
	WindowDays        int32  `json:"window_days"`
	IssuesScanned     int    `json:"issues_scanned"`
	IssuesAnalyzed    int    `json:"issues_analyzed"`
	ProposalsCreated  int    `json:"proposals_created"`
	ProposalsMerged   int    `json:"proposals_merged"`
	DuplicatesSkipped int    `json:"duplicates_skipped"`
	SanitizedSkipped  int    `json:"sanitized_skipped"`
}

// RunWorkspace scans one workspace's completed-issue window and drops drafts
// into the pool. It returns ErrNoConfig when the workspace has no config row.
func (r *Runner) RunWorkspace(ctx context.Context, workspaceID string, trigger string) (*RunStats, error) {
	now := r.now()
	cfg, err := r.Queries.GetRetrospectiveConfig(ctx, util.MustParseUUID(workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoConfig
		}
		return nil, fmt.Errorf("retrospective config: %w", err)
	}
	if !cfg.Enabled {
		return nil, ErrNoConfig
	}

	windowEnd := now
	windowStart := now.AddDate(0, 0, -int(cfg.WindowDays))
	run, err := r.Queries.InsertRetrospectiveRun(ctx, db.InsertRetrospectiveRunParams{
		WorkspaceID: util.MustParseUUID(workspaceID),
		Trigger:     trigger,
		WindowStart: pgtype.Timestamptz{Time: windowStart, Valid: true},
		WindowEnd:   pgtype.Timestamptz{Time: windowEnd, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("insert retrospective run: %w", err)
	}
	runID := util.UUIDToString(run.ID)
	stats := &RunStats{WorkspaceID: workspaceID, RunID: runID, WindowDays: cfg.WindowDays}

	finish := func(status, errMsg string) error {
		detail, _ := json.Marshal(map[string]any{
			"sanitized_skipped": stats.SanitizedSkipped,
			"llm_configured":    r.LLM != nil && r.LLM.Enabled(),
		})
		_, ferr := r.Queries.FinishRetrospectiveRun(ctx, db.FinishRetrospectiveRunParams{
			ID:                run.ID,
			WorkspaceID:       run.WorkspaceID,
			Status:            status,
			IssuesScanned:     int32(stats.IssuesScanned),
			IssuesAnalyzed:    int32(stats.IssuesAnalyzed),
			ProposalsCreated:  int32(stats.ProposalsCreated),
			ProposalsMerged:   int32(stats.ProposalsMerged),
			DuplicatesSkipped: int32(stats.DuplicatesSkipped),
			Error:             r.redact(errMsg),
			Detail:            detail,
		})
		if ferr != nil {
			return fmt.Errorf("finish retrospective run: %w", ferr)
		}
		return nil
	}

	if r.LLM == nil || !r.LLM.Enabled() {
		stats.IssuesScanned = r.countCandidates(ctx, workspaceID, cfg.IncludeInReview, windowStart, windowEnd)
		if err := finish("failed", "LLM 未配置：请在自进化 → 每日总复盘 → LLM 配置中保存配置后重试"); err != nil {
			return stats, err
		}
		return stats, nil
	}

	statuses := []string{"done"}
	if cfg.IncludeInReview {
		statuses = append(statuses, "in_review")
	}

	analyzed := 0
	for _, status := range statuses {
		issues, err := r.Queries.ListIssuesCompletedInWindow(ctx, db.ListIssuesCompletedInWindowParams{
			WorkspaceID: util.MustParseUUID(workspaceID),
			Status:      status,
			UpdatedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
			UpdatedAt_2: pgtype.Timestamptz{Time: windowEnd, Valid: true},
		})
		if err != nil {
			return stats, finish("failed", fmt.Sprintf("扫描窗口失败: %v", err))
		}
		stats.IssuesScanned += len(issues)

		for _, issue := range issues {
			if analyzed >= IssuesPerRun {
				break
			}
			issueID := util.UUIDToString(issue.ID)
			// Idempotency watermark first: already-analyzed issues are
			// invisible to this run, whatever put them in the window.
			watermarked, err := r.Queries.HasRetrospectiveWatermark(ctx, db.HasRetrospectiveWatermarkParams{
				WorkspaceID: util.MustParseUUID(workspaceID),
				IssueID:     issue.ID,
			})
			if err != nil {
				return stats, finish("failed", fmt.Sprintf("水位查询失败: %v", err))
			}
			if watermarked == 1 {
				continue
			}

			if err := r.analyzeIssue(ctx, workspaceID, runID, issueID, issue.Title, issue.Description, stats); err != nil {
				return stats, finish("failed", fmt.Sprintf("分析 Issue %s 失败: %v", issueID, err))
			}
			analyzed++
			stats.IssuesAnalyzed++
		}
		if analyzed >= IssuesPerRun {
			break
		}
	}

	if err := finish("succeeded", ""); err != nil {
		return stats, err
	}
	return stats, nil
}

// redact replaces every occurrence of a resolved secret in msg (RUYI-552).
// Every error string that reaches the run record goes through this, so an
// upstream failure echoing the API key back still stores nothing sensitive.
func (r *Runner) redact(msg string) string {
	for _, s := range r.Redact {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return msg
}

// analyzeIssue runs one issue through the pipeline: gather content →
// sanitize → LLM extract → two-layer dedup → pool insert + watermark in one
// transaction.
func (r *Runner) analyzeIssue(ctx context.Context, workspaceID, runID, issueID, title string, description pgtype.Text, stats *RunStats) error {
	input := buildIssueInput(title, description)
	comments, err := r.Queries.ListIssueCommentsForRetrospective(ctx, util.MustParseUUID(issueID))
	if err != nil {
		return fmt.Errorf("读取评论: %w", err)
	}
	for _, c := range comments {
		who := "member"
		if c.AuthorType == "agent" {
			who = "agent"
		}
		input += fmt.Sprintf("\n[%s] %s", who, truncateRunes(c.Content, MaxCommentChars))
		if len(input) >= MaxInputChars {
			break
		}
	}
	input = truncateRunes(input, MaxInputChars)

	// Credentials scan: the assembled content is about to leave for the
	// model. A refusal skips the issue (counted, not fatal) — it never
	// reaches upstream with the finding intact.
	if scan := promptscan.Scan(input); !scan.OK() {
		stats.SanitizedSkipped++
		slog.Warn("retrospective: issue content refused by promptscan, skipping",
			"issue_id", issueID, "findings", len(scan.Findings))
		return nil
	}

	drafts, err := r.extractDrafts(ctx, input)
	if err != nil {
		return fmt.Errorf("提炼草案: %w", err)
	}
	if len(drafts) == 0 {
		// Analyzed, nothing worth proposing — still watermark so the issue
		// is never re-read.
		return r.watermark(ctx, workspaceID, issueID, runID)
	}

	// Current-clause pre-check source: the workspace carrier's effective
	// content. Retrospective drafts always target the workspace carrier —
	// the issue carries no project/squad/agent attribution, and a draft
	// aimed at the wrong carrier is worse than one aimed at the base.
	current, err := r.Queries.GetWorkspacePromptContent(ctx, util.MustParseUUID(workspaceID))
	if err != nil {
		return fmt.Errorf("读取现行载体内容: %w", err)
	}
	liveClauses := map[string]bool{}
	for _, c := range legislation.Clauses(current) {
		liveClauses[strings.ToLower(c)] = true
	}

	for _, d := range drafts {
		if d.ClauseName == "" || len([]rune(d.ClauseName)) > maxClauseNameRunes {
			stats.DuplicatesSkipped++
			continue
		}
		if d.ChangeKind == "add_clause" && liveClauses[strings.ToLower(d.ClauseName)] {
			// 现行条款重复预检: the clause already exists in the carrier.
			stats.DuplicatesSkipped++
			continue
		}

		anchors, _ := json.Marshal([]map[string]string{{
			"issue_id": issueID, "ref": d.EvidenceRef, "note": truncateRunes(d.EvidenceNote, 200),
		}})
		audit, _ := json.Marshal([]map[string]any{{
			"action": "created", "actor_type": "system", "actor_id": runID,
			"at": r.now().UTC().Format(time.RFC3339),
		}})

		// Two-layer dedup, one transaction per draft: pool merge beats a
		// duplicate row; watermark rides along so a crash cannot re-analyze.
		err := r.withTx(ctx, func(q *db.Queries) error {
			existing, err := q.FindMergablePromptProposal(ctx, db.FindMergablePromptProposalParams{
				WorkspaceID:    util.MustParseUUID(workspaceID),
				CarrierScope:   "workspace",
				CarrierScopeID: util.MustParseUUID(workspaceID),
				ChangeKind:     d.ChangeKind,
				ClauseName:     d.ClauseName,
			})
			if err == nil {
				cand, _ := json.Marshal([]map[string]string{{"issue_id": issueID, "run_id": runID}})
				_, err = q.MergePromptProposalEvidence(ctx, db.MergePromptProposalEvidenceParams{
					ID:         existing.ID,
					Anchors:    anchors,
					MergedFrom: cand,
					Audit:      audit,
				})
				if err != nil {
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
			created, err := q.CreatePromptProposal(ctx, db.CreatePromptProposalParams{
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
			})
			if err != nil {
				return err
			}
			_ = created
			stats.ProposalsCreated++
			return q.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
				WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
			})
		})
		if err != nil {
			return err
		}
	}

	// Drafts were proposed or all skipped — watermark either way.
	if stats.ProposalsCreated == 0 && stats.ProposalsMerged == 0 {
		return r.watermark(ctx, workspaceID, issueID, runID)
	}
	return nil
}

func (r *Runner) watermark(ctx context.Context, workspaceID, issueID, runID string) error {
	return r.Queries.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
		WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
	})
}

func (r *Runner) countCandidates(ctx context.Context, workspaceID string, includeInReview bool, start, end time.Time) int {
	statuses := []string{"done"}
	if includeInReview {
		statuses = append(statuses, "in_review")
	}
	n := 0
	for _, status := range statuses {
		issues, err := r.Queries.ListIssuesCompletedInWindow(ctx, db.ListIssuesCompletedInWindowParams{
			WorkspaceID: util.MustParseUUID(workspaceID), Status: status,
			UpdatedAt:   pgtype.Timestamptz{Time: start, Valid: true},
			UpdatedAt_2: pgtype.Timestamptz{Time: end, Valid: true},
		})
		if err != nil {
			return n
		}
		n += len(issues)
	}
	return n
}

func (r *Runner) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(r.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// --- LLM extraction ---

// extractedDraft mirrors one draft in the model's JSON reply.
type extractedDraft struct {
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

const extractSystem = `你是 Prompt 立法管线的草案提炼器。输入是若干已完成 Issue 的真实执行内容（标题、描述、讨论）。请从中提炼 Prompt 改进草案：条款必须写成可直接落库的最终文本（不带修订痕迹、不用弱表述），每条草案必须给出内容闸五答（层次归属 layer / 去留判据 retention / 代价声明 cost / 同主题冲突裁决 conflict / 重复检查结论 dedup）与证据锚。没有值得提炼的内容就返回空数组。只输出 JSON 对象，形如：
{"drafts":[{"carrier_scope":"workspace","target_section":"章节名或空串","change_kind":"add_clause|revise_clause|remove_clause","clause_name":"条款名","clause_text":"- **条款名**：条款正文","gate_answers":{"layer":"...","retention":"...","cost":"...","conflict":"...","dedup":"..."},"evidence_ref":"issue 标题或评论摘要","evidence_note":"一句话说明依据"}]}`

func (r *Runner) extractDrafts(ctx context.Context, input string) ([]extractedDraft, error) {
	ctx, cancel := context.WithTimeout(ctx, LLMTimeout)
	defer cancel()
	raw, err := r.LLM.GenerateJSON(ctx, r.Model, extractSystem, input, 0.2, 4096)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Drafts []extractedDraft `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("模型输出不是合法 JSON: %w", err)
	}
	if len(parsed.Drafts) > MaxDraftsPerIssue {
		parsed.Drafts = parsed.Drafts[:MaxDraftsPerIssue]
	}
	// Normalize: retrospective drafts target the workspace carrier only, and
	// unknown change kinds are dropped rather than half-validated later.
	kept := parsed.Drafts[:0]
	for _, d := range parsed.Drafts {
		switch d.ChangeKind {
		case "add_clause", "revise_clause", "remove_clause":
		default:
			continue
		}
		if d.ChangeKind != "remove_clause" && strings.TrimSpace(d.ClauseText) == "" {
			continue
		}
		kept = append(kept, d)
	}
	return kept, nil
}

// buildIssueInput assembles the model's user prompt head: title, description,
// status-independent (the window already selected completion).
func buildIssueInput(title string, description pgtype.Text) string {
	var b strings.Builder
	b.WriteString("Issue 标题：")
	b.WriteString(title)
	if description.Valid && strings.TrimSpace(description.String) != "" {
		b.WriteString("\n描述：")
		b.WriteString(truncateRunes(description.String, MaxCommentChars))
	}
	b.WriteString("\n讨论与进度：")
	return b.String()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ErrNoConfig marks "nothing to do" — no config row, or not enabled. The
// scheduler job treats it as a silent skip; the manual trigger surfaces it
// to the owner as a 400.
var ErrNoConfig = errors.New("retrospective: workspace config missing or disabled")
