package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/clausemigration"
	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/pkg/clausemark"
)

// TestClauseMigrationDrill is the 拆单 5 legacy-migration rehearsal (ADR 007
// §8): the RUYI-733 audit inventory's two pilot clauses (成本纪律: 禁止考古
// 式取证 / 禁止元工作 — both single-line bold-label rules, independently
// repealable, so both pass the §8 migration criteria) are adopted as clause
// assets in two batches, each with an independent rollback, and every step
// is checked byte-for-byte:
//
//	drift clean → batch 1 enact → snapshot byte-verify → batch 2 enact →
//	cumulative byte-verify → claims record injections → reconciliation
//	healthy → forged violation flagged (checker red) → cleanup (green) →
//	rollback batch 2 → byte-exact restoration → rollback batch 1 → no
//	markers left, both rule texts back verbatim → re-rollback refused.
//
// The restored placement is the end of the recorded section (documented
// deterministic semantics, not the original line position).
func TestClauseMigrationDrill(t *testing.T) {
	f := newClauseInjectionFixture(t)

	// The rehearsal corpus mirrors the governed carrier shape with the two
	// pilot lines inserted verbatim in 成本纪律.
	dbfx.Exec(t, `UPDATE workspace SET context = $1 WHERE id = $2`, drillCarrierText, f.wsID)

	pilot := pilotManifest(t)

	// ── drift: inventory vs live carrier text ────────────────────────────
	findings := clausemigration.CheckDrift(pilot, map[string]string{"workspace": drillCarrierText})
	if !clausemigration.DriftClean(findings) {
		t.Fatalf("pilot manifest drifts against the rehearsal corpus: %+v", findings)
	}

	pre := drillCarrierText
	batch1 := pilot.Bodies(1)
	batch2 := pilot.Bodies(2)
	both := append(append([]string{}, batch1...), batch2...)

	// ── batch 1: draft → pending_owner → enacted ─────────────────────────
	clauseID1 := enactMigrateEntry(t, f, pilot.Entries[0])
	after1 := readCarrierContext(t, f.wsID)
	if err := clausemigration.VerifySnapshot(pre, clausemark.Render(after1, []clausemark.RenderedClause{{Slug: activeSlug(t, clauseID1), Version: 1, Body: pilot.Entries[0].Body}}), batch1); err != nil {
		t.Fatalf("batch 1 runtime composition: %v", err)
	}
	assertClauseV1Content(t, clauseID1, pilot.Entries[0].Body)

	// ── batch 2, verified against the cumulative migration ───────────────
	clauseID2 := enactMigrateEntry(t, f, pilot.Entries[1])
	after2 := readCarrierContext(t, f.wsID)
	if err := clausemigration.VerifySnapshot(pre, clausemark.Render(after2, []clausemark.RenderedClause{
		{Slug: activeSlug(t, clauseID1), Version: 1, Body: pilot.Entries[0].Body},
		{Slug: activeSlug(t, clauseID2), Version: 1, Body: pilot.Entries[1].Body},
	}), both); err != nil {
		t.Fatalf("batch 2 runtime composition: %v", err)
	}
	assertClauseV1Content(t, clauseID2, pilot.Entries[1].Body)

	// ── claims: injection rows ride the real claim route ─────────────────
	// One pending (queued/dispatched) task per (issue, agent): task 1
	// completes before task 2 is queued, matching the real per-batch rhythm.
	taskID1, wsCtx1 := enqueueAndClaim(t, f)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, taskID1)
	_, wsCtx2 := enqueueAndClaim(t, f)
	for _, ctxText := range []string{wsCtx1, wsCtx2} {
		if strings.Count(ctxText, "<!-- clause:id=") != 2 {
			t.Fatalf("claimed context should carry both clause segments:\n%s", ctxText)
		}
	}
	var injectedTasks int
	dbfx.QueryRow(t, `SELECT count(DISTINCT task_id) FROM clause_injection WHERE workspace_id = $1 AND carrier_scope = 'workspace'`,
		f.wsID).Scan(&injectedTasks)
	if injectedTasks != 2 {
		t.Fatalf("injected tasks = %d, want 2", injectedTasks)
	}

	// ── reconciliation healthy before the rollback walk ──────────────────
	recon := getReconciliation(t, f)
	if !recon.Healthy || len(recon.Violations) != 0 || len(recon.DriftCarriers) != 0 {
		t.Fatalf("reconciliation after migration: healthy=%v violations=%d drift=%d",
			recon.Healthy, len(recon.Violations), len(recon.DriftCarriers))
	}

	// ── checker red side: a forged layer-set violation must surface ──────
	// The write path records every injected scope, so a squad-scope row on a
	// task attributed only to the workspace models post-hoc attribution
	// drift; the ⊆ checker must fail this world (fail-first evidence).
	forge := struct{ taskID string }{}
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, priority, attempt, completed_at, prompt_versions)
		VALUES ($1, $2, 'completed', 0, 1, now(), $3::jsonb) RETURNING id`,
		f.agentID, f.issueID, `{"workspace": "`+f.wsID+`"}`).Scan(&forge.taskID)
	dbfx.Exec(t, `
		INSERT INTO clause_injection (workspace_id, task_id, clause_id, clause_version, carrier_scope)
		VALUES ($1, $2, $3, 1, 'squad')`, f.wsID, forge.taskID, clauseID1)
	recon = getReconciliation(t, f)
	if recon.Healthy || len(recon.Violations) != 1 || recon.Violations[0].CarrierScope != "squad" {
		t.Fatalf("forged violation not flagged: healthy=%v violations=%+v", recon.Healthy, recon.Violations)
	}
	dbfx.Exec(t, `DELETE FROM clause_injection WHERE task_id = $1`, forge.taskID)
	dbfx.Exec(t, `DELETE FROM agent_task_queue WHERE id = $1`, forge.taskID)
	recon = getReconciliation(t, f)
	if !recon.Healthy {
		t.Fatalf("reconciliation not healthy again after cleanup: %+v", recon)
	}

	// ── rollback batch 2: withdraw + restore + re-render, one tx ─────────
	res2, err := testHandler.RollbackClauseMigrationBatch(context.Background(), parseUUID(f.wsID),
		[]ClauseMigrationRollbackItem{{ClauseID: parseUUID(clauseID2), Section: pilot.Entries[1].Section}},
		"owner", f.ownerID, "RUYI-764 迁移回滚：批 2（试点演练）")
	if err != nil {
		t.Fatalf("rollback batch 2: %v", err)
	}
	afterRoll2 := readCarrierContext(t, f.wsID)

	// Byte expectation: batch-1 stable context, batch-2 body restored at the
	// end of 成本纪律, remaining clause (batch 1) re-rendered at the tail.
	wantStable2, err := legislation.RestoreBlockToSection(clausemark.StripMarkers(after2), pilot.Entries[1].Section, pilot.Entries[1].Body)
	if err != nil {
		t.Fatalf("compose expected stable: %v", err)
	}
	want2 := clausemark.Static(wantStable2, []clausemark.RenderedClause{
		{Slug: activeSlug(t, clauseID1), Version: 1, Body: pilot.Entries[0].Body},
	})
	if afterRoll2 != want2 {
		t.Fatalf("rollback batch 2 text mismatch:\n--- got ---\n%s\n--- want ---\n%s", afterRoll2, want2)
	}
	if len(res2) == 0 || res2["workspace/"+f.wsID] == "" {
		t.Fatalf("rollback evidence missing: %v", res2)
	}
	assertBindingWithdrawn(t, clauseID2)
	assertRollbackVersionRecorded(t, f.wsID)

	// ── rollback batch 1: carrier back to pre-migration semantics ────────
	if _, err := testHandler.RollbackClauseMigrationBatch(context.Background(), parseUUID(f.wsID),
		[]ClauseMigrationRollbackItem{{ClauseID: parseUUID(clauseID1), Section: pilot.Entries[0].Section}},
		"owner", f.ownerID, "RUYI-764 迁移回滚：批 1（试点演练）"); err != nil {
		t.Fatalf("rollback batch 1: %v", err)
	}
	afterRoll1 := readCarrierContext(t, f.wsID)

	if markers := clausemark.ScanMarkers(afterRoll1); len(markers) != 0 {
		t.Fatalf("markers remain after full rollback: %+v", markers)
	}
	for i, e := range pilot.Entries {
		block := legislation.FindClauseBlock(afterRoll1, e.Title)
		if block != e.Body {
			t.Fatalf("entry %d body not restored byte-exact:\n got %q\nwant %q", i, block, e.Body)
		}
	}
	// afterRoll2 is the post-batch-2-rollback text: the meta-work rule is
	// already back in the stable half, so only batch 1 needs restoring.
	wantStable1, err := legislation.RestoreBlockToSection(clausemark.StripMarkers(afterRoll2), pilot.Entries[0].Section, pilot.Entries[0].Body)
	if err != nil {
		t.Fatalf("compose expected stable (restore batch 1): %v", err)
	}
	if err != nil {
		t.Fatalf("compose expected stable 1: %v", err)
	}
	if afterRoll1 != clausemark.Static(wantStable1, nil) {
		t.Fatalf("full rollback text mismatch:\n--- got ---\n%s\n--- want ---\n%s", afterRoll1, clausemark.Static(wantStable1, nil))
	}
	recon = getReconciliation(t, f)
	if !recon.Healthy || len(recon.DriftCarriers) != 0 {
		t.Fatalf("reconciliation after full rollback: healthy=%v drift=%+v", recon.Healthy, recon.DriftCarriers)
	}

	// ── idempotency by refusal: an already-rolled-back batch errors ──────
	if _, err := testHandler.RollbackClauseMigrationBatch(context.Background(), parseUUID(f.wsID),
		[]ClauseMigrationRollbackItem{{ClauseID: parseUUID(clauseID1), Section: pilot.Entries[0].Section}},
		"owner", f.ownerID, "重复回滚应被拒绝"); err == nil {
		t.Fatal("second rollback of batch 1 must fail (no active binding), got nil")
	}
}

// ── drill helpers ───────────────────────────────────────────────────────────

// drillCarrierText is the rehearsal carrier: the governed corpus shape plus
// the two pilot lines byte-identical to the inventory bodies.
var drillCarrierText = strings.Join([]string{
	"# 平台协作规范",
	"",
	"## 沟通规范",
	"",
	"- **行文基调**：专业、正式、准确、简明扼要。",
	"- **先结论后依据**：第一句给出结论，依据随后。",
	"",
	"## 成本纪律",
	"",
	"- **合并取数**：能一条命令取得的情报不拆多轮。",
	"- **禁止叙述轮**：工具编排期间不带工具调用的纯文字轮禁止出现。",
	pilotBodyArchaeology,
	pilotBodyMetaWork,
	"",
	"## 成员 UUID 索引",
	"",
	"| 成员 | UUID |",
	"| --- | --- |",
	"| 示例 | 00000000-0000-0000-0000-000000000000 |",
	"",
}, "\n")

// The pilot bodies are the RUYI-733 inventory lines, byte-identical to the
// ruyi workspace context.md 成本纪律 section they migrate.
const pilotBodyArchaeology = "- **禁止考古式取证**：决策依据只来自当前 Issue、登记文档、项目记忆与平台数据；禁止检索会话转录目录（如 `~/.claude/projects/`）或历史 run 原文挖掘依据。"
const pilotBodyMetaWork = "- **禁止元工作**：执行中禁止查询本单/自身的 token 用量与运行统计；绩效取数按 Leader 角色条款仅在处置/收尾评论时进行一次。"

// pilotManifest builds the pilot inventory (the tool's real input format)
// and round-trips it through LoadManifest so the decode-path validation is
// exercised on every drill run.
func pilotManifest(t *testing.T) *clausemigration.Manifest {
	t.Helper()
	m := clausemigration.Manifest{
		Source: "RUYI-733 审计清单 · 试点批（ADR 007 拆单 5 / RUYI-764）",
		Entries: []clausemigration.Entry{
			{
				ID: "cost-no-archaeology", Title: "禁止考古式取证", Scope: "workspace",
				Section: "成本纪律", Body: pilotBodyArchaeology, Batch: 1,
				AuditItem: "RUYI-733 成本纪律·禁止考古式取证",
				Rationale: "可独立废止；单行标签块；废止后不影响 workspace 身份与协议理解（ADR 007 §8 判据）",
				Answers: clausemigration.GateAnswers{
					Layer:     "workspace=成本纪律（审计取证类禁令）",
					Retention: "每轮输出都适用，留底座",
					Cost:      "约 210 字符常驻",
					Conflict:  "与取证红线类规则同主题，无重复无冲突",
					Dedup:     "已对照 workspace 上下文全文与手册分册，无重复条款",
				},
			},
			{
				ID: "cost-no-meta-work", Title: "禁止元工作", Scope: "workspace",
				Section: "成本纪律", Body: pilotBodyMetaWork, Batch: 2,
				AuditItem: "RUYI-733 成本纪律·禁止元工作",
				Rationale: "可独立废止；单行标签块；废止后不影响 workspace 身份与协议理解（ADR 007 §8 判据）",
				Answers: clausemigration.GateAnswers{
					Layer:     "workspace=成本纪律（元工作禁令）",
					Retention: "每轮输出都适用，留底座",
					Cost:      "约 170 字符常驻",
					Conflict:  "与成本纪律类规则同主题，无重复无冲突",
					Dedup:     "已对照 workspace 上下文全文与手册分册，无重复条款",
				},
			},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal pilot manifest: %v", err)
	}
	loaded, err := clausemigration.LoadManifest(data)
	if err != nil {
		t.Fatalf("pilot manifest invalid: %v", err)
	}
	return loaded
}

// enactMigrateEntry walks one migrate_clause proposal through the real
// API: member drafts it, submits, owner approves (gate + enact), and the
// row's clause_id is the created asset.
func enactMigrateEntry(t *testing.T, f *clauseInjectionFixture, e clausemigration.Entry) string {
	t.Helper()
	req := legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals",
		clausemigration.ProposalBody(e, f.wsID))
	code, body := legislationCall(t, testHandler.CreatePromptProposal, req)
	if code != http.StatusCreated {
		t.Fatalf("create migrate draft %s: expected 201, got %d: %s", e.ID, code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode created proposal: %v (%s)", err, body)
	}
	submitDraft(t, f.wsID, f.memberID, created.ID)
	if code, body := ownerApprove(t, f.ownerID, f.wsID, created.ID); code != http.StatusOK {
		t.Fatalf("enact migrate %s: expected 200, got %d: %s", e.ID, code, body)
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`,
		f.wsID, created.ID).Scan(&clauseID)
	if clauseID == "" {
		t.Fatalf("enacted migrate proposal %s has no clause_id", e.ID)
	}
	return clauseID
}

func readCarrierContext(t *testing.T, wsID string) string {
	t.Helper()
	var ctxText string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&ctxText)
	return ctxText
}

func assertClauseV1Content(t *testing.T, clauseID, want string) {
	t.Helper()
	var got string
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 1`, clauseID).Scan(&got)
	if got != want {
		t.Fatalf("clause v1 content mismatch:\n got %q\nwant %q", got, want)
	}
}

func activeSlug(t *testing.T, clauseID string) string {
	t.Helper()
	var slug string
	dbfx.QueryRow(t, `SELECT slug FROM clause WHERE id = $1`, clauseID).Scan(&slug)
	return slug
}

func assertBindingWithdrawn(t *testing.T, clauseID string) {
	t.Helper()
	var bindingStatus, clauseStatus string
	dbfx.QueryRow(t, `SELECT status FROM clause_binding WHERE clause_id = $1 ORDER BY created_at DESC LIMIT 1`, clauseID).Scan(&bindingStatus)
	dbfx.QueryRow(t, `SELECT status FROM clause WHERE id = $1`, clauseID).Scan(&clauseStatus)
	if bindingStatus != "withdrawn" {
		t.Fatalf("binding status = %s, want withdrawn", bindingStatus)
	}
	if clauseStatus != "deprecated" {
		t.Fatalf("clause status = %s, want deprecated", clauseStatus)
	}
}

func assertRollbackVersionRecorded(t *testing.T, wsID string) {
	t.Helper()
	var n int
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_version WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id = $1
		AND source = 'legislation' AND change_note LIKE '%迁移回滚%'`, wsID).Scan(&n)
	if n == 0 {
		t.Fatal("rollback did not record a legislation prompt_version with a 迁移回滚 change note")
	}
}

// reconciliationView is the slice of the reconciliation payload the drill
// asserts on.
type reconciliationView struct {
	Healthy    bool `json:"healthy"`
	Violations []struct {
		TaskID       string `json:"task_id"`
		CarrierScope string `json:"carrier_scope"`
	} `json:"layer_set_violations"`
	DriftCarriers []struct {
		Scope string `json:"scope"`
	} `json:"drift_carriers"`
}

func getReconciliation(t *testing.T, f *clauseInjectionFixture) reconciliationView {
	t.Helper()
	code, body := legislationCall(t, testHandler.GetClauseReconciliation,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/reconciliation", nil))
	if code != http.StatusOK {
		t.Fatalf("reconciliation: expected 200, got %d: %s", code, body)
	}
	var view reconciliationView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode reconciliation: %v (%s)", err, body)
	}
	return view
}
