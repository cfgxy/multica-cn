package handler

// Clause asset write path (RUYI-751 / ADR 007 §8): everything the enacted
// transaction touches beyond the proposal row itself — the clause /
// clause_version / clause_binding assets, the carrier business-column
// re-render, and the carrier version rows the re-render funnels through the
// same governance ledger every other effective-content write uses.
//
// Fail-closed: any error returned here aborts the whole enact transaction —
// assets, business columns, versions and baselines roll back together, so a
// half-written enactment is unrepresentable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptscan"
)

// validateClauseConditions checks a draft's conditions JSON against the ADR
// 007 §1 minimal shape: NULL/absent = unconditional, otherwise an object
// whose optional task_kinds array whitelists known task kinds. Returns a
// user-facing message or "". The value domain lives in clauseTaskKinds
// (clause_injection.go — the claim side shares it).
func validateClauseConditions(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "conditions must be a JSON object"
	}
	var parsed struct {
		TaskKinds []string `json:"task_kinds"`
	}
	if tk, ok := obj["task_kinds"]; ok && string(tk) != "null" {
		if err := json.Unmarshal(tk, &parsed.TaskKinds); err != nil {
			return "conditions.task_kinds must be a string array"
		}
	}
	seen := map[string]bool{}
	for _, k := range parsed.TaskKinds {
		if !clauseTaskKinds[k] {
			return fmt.Sprintf("conditions.task_kinds 含未知任务类型 %q", k)
		}
		if seen[k] {
			return fmt.Sprintf("conditions.task_kinds 重复项 %q", k)
		}
		seen[k] = true
	}
	return ""
}

// conditionsJSON normalizes a validated conditions payload for storage:
// empty/null becomes SQL NULL (全适用), otherwise the object as authored.
func conditionsJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	return []byte(raw)
}

// parseNullableUUID parses an optional UUID string; "" stays an invalid
// (NULL) UUID rather than erroring — move_to_scope_id is absent on every
// non-move proposal.
func parseNullableUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	return parseUUID(s)
}

// resolvedClause is the clause identity an enactment acts on.
type resolvedClause struct {
	ID      pgtype.UUID
	Slug    string
	Title   string
	Status  string
	Version int32 // newest clause_version at resolution time
}

// resolveProposalClauseTx finds the clause a revise/remove/move proposal
// acts on: by the reference key when set (the post-970 shape), else by the
// display title (legacy name-only rows — ADR 007 §8 allows the fallback
// during the compat window). Ambiguity or absence is a fail-closed error.
func resolveProposalClauseTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, clauseID pgtype.UUID, clauseName string) (resolvedClause, error) {
	if clauseID.Valid {
		row, err := qtx.GetClauseByID(ctx, db.GetClauseByIDParams{ID: clauseID, WorkspaceID: workspaceID})
		if err != nil {
			return resolvedClause{}, fmt.Errorf("引用的条款不存在（clause_id=%s）", uuidToString(clauseID))
		}
		return resolveClauseVersion(ctx, qtx, row)
	}
	name := strings.Join(strings.Fields(clauseName), " ")
	rows, err := qtx.ListClausesByTitle(ctx, db.ListClausesByTitleParams{WorkspaceID: workspaceID, Title: name})
	if err != nil {
		return resolvedClause{}, err
	}
	switch len(rows) {
	case 0:
		return resolvedClause{}, fmt.Errorf("条款「%s」不存在于条款资产库", name)
	case 1:
		return resolveClauseVersion(ctx, qtx, rows[0])
	default:
		return resolvedClause{}, fmt.Errorf("条款名「%s」匹配到 %d 个条款资产，请在提案中写明 clause_id", name, len(rows))
	}
}

func resolveClauseVersion(ctx context.Context, qtx *db.Queries, row db.Clause) (resolvedClause, error) {
	out := resolvedClause{ID: row.ID, Slug: row.Slug, Title: row.Title, Status: row.Status}
	v, err := qtx.LatestClauseVersion(ctx, row.ID)
	if err == nil {
		out.Version = v
	}
	return out, nil
}

// clauseAudit appends one entry to a binding's audit trail.
func clauseAudit(action, actorType, actorID string, extra map[string]any) []byte {
	entry := map[string]any{
		"action": action, "actor_type": actorType, "actor_id": actorID,
		"at": time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range extra {
		entry[k] = v
	}
	blob, _ := json.Marshal([]map[string]any{entry})
	return blob
}

// clauseEnactOutcome carries what the asset writes produced, for the
// proposal row and the caller's response.
type clauseEnactOutcome struct {
	ClauseID        pgtype.UUID
	Slug            string
	Version         int32 // the clause_version this enactment produced (0 for remove/move)
	RenderOverrides map[carrierRef]*clauseRenderOverride
	// Affected lists the carrier scopes whose business column must re-render.
	Affected map[string]bool
}

// affectedCarriers resolves the (scope, scope_id) pairs a proposal touches.
// A plain proposal touches its own carrier; a move touches source and
// destination.
func (o clauseEnactOutcome) carrierRefs(p db.PromptProposal) []carrierRef {
	refs := []carrierRef{{scope: p.CarrierScope, id: p.CarrierScopeID}}
	if p.ChangeKind == "move_clause" && p.MoveToScope.Valid && p.MoveToScopeID.Valid {
		refs = append(refs, carrierRef{scope: p.MoveToScope.String, id: p.MoveToScopeID})
	}
	return refs
}

type carrierRef struct {
	scope string
	id    pgtype.UUID
}

// clauseRenderOverride supplies a not-yet-persisted version of one active
// clause. It lets previews and gate synthesis use the same asset renderer as
// the post-write path without requiring a speculative database write.
type clauseRenderOverride struct {
	ClauseID          pgtype.UUID
	Version           int32
	Content           string
	SupersededContent string
}

// renderedClausesByCarrier partitions the active asset set by the complete
// binding target. A scope alone is not enough: two project, squad, or agent
// carriers in the same workspace must never receive each other's clauses.
func renderedClausesByCarrier(rows []db.ListActiveWorkspaceClausesRow, override *clauseRenderOverride) map[carrierRef][]clausemark.RenderedClause {
	byCarrier := make(map[carrierRef][]clausemark.RenderedClause)
	for _, row := range rows {
		version, content := row.Version, row.Content
		var supersededBodies []string
		if override != nil && row.ClauseID == override.ClauseID {
			// A persisted write only needs the old body removed. A preview also
			// supplies the prospective content and version before the write exists.
			if override.Content != "" {
				version, content = override.Version, override.Content
			}
			if override.SupersededContent != "" && override.SupersededContent != content {
				supersededBodies = []string{override.SupersededContent}
			}
		}
		ref := carrierRef{scope: row.Scope, id: row.ScopeID}
		byCarrier[ref] = append(byCarrier[ref], clausemark.RenderedClause{
			Slug: row.Slug, Version: int(version), Body: content, SupersededBodies: supersededBodies,
		})
	}
	return byCarrier
}

func (h *Handler) renderCarrierFromActiveClausesTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, ref carrierRef, current string, override *clauseRenderOverride) (string, error) {
	rows, err := qtx.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("有效条款集读取失败: %w", err)
	}
	return clausemark.Render(current, renderedClausesByCarrier(rows, override)[ref]), nil
}

// enactClauseAssetsTx writes the clause-family rows for one enacted
// proposal. The proposal row must still be flipped by EnactPromptProposal
// afterwards — every failure here returns an error so the enclosing
// transaction rolls the whole enactment back.
func (h *Handler) enactClauseAssetsTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, p db.PromptProposal, actorType, actorID string) (clauseEnactOutcome, error) {
	out := clauseEnactOutcome{
		Affected:        map[string]bool{},
		RenderOverrides: map[carrierRef]*clauseRenderOverride{},
	}

	switch p.ChangeKind {
	case "add_clause", "migrate_clause":
		// migrate_clause (ADR 007 §8 存量迁移) shares the add_clause asset
		// write: by the time the proposal reaches this switch its clause_text
		// has been byte-verified against the carrier block by the gate
		// (synthesizeMigrate), so adopting it as clause v1 content is exactly
		// "the existing text becomes the asset".
		slug, err := clausemark.NewSlug()
		if err != nil {
			return out, err
		}
		clause, err := qtx.CreateClause(ctx, db.CreateClauseParams{
			WorkspaceID:   workspaceID,
			Slug:          slug,
			Title:         strings.Join(strings.Fields(p.ClauseName), " "),
			CreatedByType: actorType,
			CreatedByID:   parseUUID(actorID),
		})
		if err != nil {
			return out, fmt.Errorf("条款资产创建失败: %w", err)
		}
		content := strings.TrimRight(p.ClauseText, "\n")
		version, err := qtx.CreateClauseVersion(ctx, db.CreateClauseVersionParams{
			ClauseID:      clause.ID,
			Version:       1,
			Content:       content,
			ContentSha256: sha256Hex(content),
			Source:        "legislation",
			CarrierScope:  p.CarrierScope,
			CreatedByType: actorType,
			CreatedByID:   parseUUID(actorID),
		})
		if err != nil {
			return out, fmt.Errorf("条款版本写入失败: %w", err)
		}
		if _, err := qtx.CreateClauseBinding(ctx, db.CreateClauseBindingParams{
			ClauseID:           clause.ID,
			Scope:              p.CarrierScope,
			ScopeID:            p.CarrierScopeID,
			Conditions:         conditionsJSON(p.Conditions),
			SinceClauseVersion: 1,
			AuditLog:           clauseAudit("binding_active", actorType, actorID, map[string]any{"proposal_id": uuidToString(p.ID)}),
		}); err != nil {
			return out, fmt.Errorf("条款绑定写入失败: %w", err)
		}
		out.ClauseID, out.Slug, out.Version = clause.ID, clause.Slug, version.Version

	case "revise_clause":
		clause, err := resolveProposalClauseTx(ctx, qtx, workspaceID, p.ClauseID, p.ClauseName)
		if err != nil {
			return out, err
		}
		if clause.Status != "active" {
			return out, fmt.Errorf("条款「%s」当前状态为 %s，仅 active 条款可修订", clause.Title, clause.Status)
		}
		binding, err := qtx.GetActiveClauseBinding(ctx, clause.ID)
		if err != nil {
			return out, fmt.Errorf("条款「%s」没有生效绑定，无法修订", clause.Title)
		}
		currentVersion, err := qtx.GetClauseVersion(ctx, db.GetClauseVersionParams{
			ClauseID: clause.ID,
			Version:  clause.Version,
		})
		if err != nil {
			return out, fmt.Errorf("条款「%s」当前版本读取失败: %w", clause.Title, err)
		}
		next := clause.Version + 1
		content := strings.TrimRight(p.ClauseText, "\n")
		if _, err := qtx.CreateClauseVersion(ctx, db.CreateClauseVersionParams{
			ClauseID:      clause.ID,
			Version:       next,
			Content:       content,
			ContentSha256: sha256Hex(content),
			Source:        "legislation",
			CarrierScope:  binding.Scope,
			CreatedByType: actorType,
			CreatedByID:   parseUUID(actorID),
		}); err != nil {
			return out, fmt.Errorf("条款版本写入失败: %w", err)
		}
		if _, err := qtx.BumpClauseBindingVersion(ctx, db.BumpClauseBindingVersionParams{
			ID:                 binding.ID,
			SinceClauseVersion: next,
			Conditions:         conditionsJSON(p.Conditions),
			Audit:              clauseAudit("version_bumped", actorType, actorID, map[string]any{"to_version": next, "proposal_id": uuidToString(p.ID)}),
		}); err != nil {
			return out, fmt.Errorf("条款绑定更新失败: %w", err)
		}
		out.ClauseID, out.Slug, out.Version = clause.ID, clause.Slug, next
		out.Affected[binding.Scope] = true
		out.RenderOverrides[carrierRef{scope: binding.Scope, id: binding.ScopeID}] = &clauseRenderOverride{
			ClauseID:          clause.ID,
			SupersededContent: currentVersion.Content,
		}

	case "remove_clause":
		clause, err := resolveProposalClauseTx(ctx, qtx, workspaceID, p.ClauseID, p.ClauseName)
		if err != nil {
			return out, err
		}
		binding, err := qtx.WithdrawClauseBinding(ctx, db.WithdrawClauseBindingParams{
			ClauseID: clause.ID,
			Audit:    clauseAudit("binding_withdrawn", actorType, actorID, map[string]any{"proposal_id": uuidToString(p.ID)}),
		})
		if err != nil {
			return out, fmt.Errorf("条款「%s」没有生效绑定，无法删除", clause.Title)
		}
		if _, err := qtx.UpdateClauseStatus(ctx, db.UpdateClauseStatusParams{
			ID: clause.ID, WorkspaceID: workspaceID, Status: "deprecated",
		}); err != nil {
			return out, fmt.Errorf("条款停用失败: %w", err)
		}
		out.ClauseID, out.Slug = clause.ID, clause.Slug
		out.Affected[binding.Scope] = true

	case "move_clause":
		if !p.MoveToScope.Valid || !p.MoveToScopeID.Valid {
			return out, errors.New("move_clause 提案缺少目标载体")
		}
		clause, err := resolveProposalClauseTx(ctx, qtx, workspaceID, p.ClauseID, p.ClauseName)
		if err != nil {
			return out, err
		}
		if clause.Status != "active" {
			return out, fmt.Errorf("条款「%s」当前状态为 %s，仅 active 条款可迁移", clause.Title, clause.Status)
		}
		if p.MoveToScope.String == p.CarrierScope && p.MoveToScopeID == p.CarrierScopeID {
			return out, errors.New("move_clause 目标载体与源载体相同")
		}
		if _, err := qtx.SupersedeClauseBinding(ctx, db.SupersedeClauseBindingParams{
			ClauseID: clause.ID,
			Audit:    clauseAudit("binding_superseded", actorType, actorID, map[string]any{"proposal_id": uuidToString(p.ID), "to_scope": p.MoveToScope.String}),
		}); err != nil {
			return out, fmt.Errorf("条款「%s」没有生效绑定，无法迁移", clause.Title)
		}
		if _, err := qtx.CreateClauseBinding(ctx, db.CreateClauseBindingParams{
			ClauseID:           clause.ID,
			Scope:              p.MoveToScope.String,
			ScopeID:            p.MoveToScopeID,
			Conditions:         conditionsJSON(p.Conditions),
			SinceClauseVersion: clause.Version,
			AuditLog:           clauseAudit("binding_active", actorType, actorID, map[string]any{"proposal_id": uuidToString(p.ID), "from_scope": p.CarrierScope}),
		}); err != nil {
			return out, fmt.Errorf("目标载体绑定写入失败: %w", err)
		}
		out.ClauseID, out.Slug = clause.ID, clause.Slug

	default:
		return out, fmt.Errorf("未知变更类型 %q", p.ChangeKind)
	}
	return out, nil
}

// rerenderAffectedCarriersTx re-renders every carrier the enactment touched:
// stable context kept, clause region rebuilt from the asset library (full
// task-kind union, no condition filtering — ADR 007 §业务列渲染快照), then the
// rendered text is secret-scanned, written into the business column AND the
// prompt_version ledger, and the structure baseline is rebuilt from the
// rendered result so the next gate run sees exactly what the carrier holds.
//
// texts maps each affected carrier to the text the re-render starts from —
// the gate's synthesized text (the proposal already applied inline). Keyed by
// the full carrier reference, not the scope name: a move_clause between two
// entities of the same scope touches two distinct rows of one tier.
func (h *Handler) rerenderAffectedCarriersTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, carriers []carrierRef, texts map[carrierRef]string, overrides map[carrierRef]*clauseRenderOverride, actorType, actorID string) error {
	if len(carriers) == 0 {
		return nil
	}
	allClauses, err := qtx.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("有效条款集读取失败: %w", err)
	}
	for _, c := range carriers {
		current, ok := texts[c]
		if !ok {
			continue
		}
		rendered := clausemark.Render(current, renderedClausesByCarrier(allClauses, overrides[c])[c])
		scan, ok, reason := promptGovernanceGate(rendered)
		if !ok {
			return fmt.Errorf("重渲染文本未通过内容门闸（%s）: %s", c.scope, reason)
		}
		if _, err := persistCarrierVersionTx(ctx, qtx, workspaceID, promptVersionScope(c.scope), c.id, rendered,
			"条款渲染快照重建", "legislation", nil, scan.Revision, scan.Findings, actorID); err != nil {
			return fmt.Errorf("载体版本写入失败（%s）: %w", c.scope, err)
		}
		sections, clauses := legislation.ExtractStructure(rendered)
		if _, err := qtx.UpsertPromptStructureBaseline(ctx, db.UpsertPromptStructureBaselineParams{
			WorkspaceID:    workspaceID,
			CarrierScope:   c.scope,
			CarrierScopeID: c.id,
			Sections:       mustJSON(sections),
			Clauses:        mustJSON(clauses),
			ContentSha256:  sha256Hex(rendered),
		}); err != nil {
			return fmt.Errorf("结构基线重建失败（%s）: %w", c.scope, err)
		}
	}
	return nil
}

// persistCarrierVersionTx is the transaction core of the governance write
// funnel: next version number, prompt_version row, business-column copy. The
// caller owns the entity row lock and the transaction; the funnel contract
// (version row + business column move together or not at all) holds inside
// one tx. Shared by the handler write path and clause re-renders.
func persistCarrierVersionTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID,
	content, changeNote, source string, sourceVer *int32, scannerRevision string, gateResult []promptscan.Finding,
	authorUserID string) (db.PromptVersion, error) {

	next, err := qtx.NextPromptVersion(ctx, db.NextPromptVersionParams{Scope: string(scope), ScopeID: scopeID})
	if err != nil {
		return db.PromptVersion{}, fmt.Errorf("version number: %w", err)
	}
	gateBlob, err := json.Marshal(gateResult)
	if err != nil || gateResult == nil {
		gateBlob = []byte("[]")
	}
	var sourceVerNarg pgtype.Int4
	if sourceVer != nil {
		sourceVerNarg = pgtype.Int4{Int32: *sourceVer, Valid: true}
	}
	created, err := qtx.CreatePromptVersion(ctx, db.CreatePromptVersionParams{
		WorkspaceID:     workspaceID,
		Scope:           string(scope),
		ScopeID:         scopeID,
		Version:         next,
		Content:         content,
		ContentSha256:   sha256Hex(content),
		Source:          source,
		SourceVersion:   sourceVerNarg,
		ChangeNote:      changeNote,
		ScannerRevision: scannerRevision,
		GateResult:      gateBlob,
		AuthorUserID:    parseUUID(authorUserID),
	})
	if err != nil {
		return db.PromptVersion{}, fmt.Errorf("version insert: %w", err)
	}
	if err := writeScopeEffectiveContentTx(ctx, qtx, scope, scopeID, content); err != nil {
		return db.PromptVersion{}, fmt.Errorf("effective content write: %w", err)
	}
	return created, nil
}

// writeScopeEffectiveContentTx is the tx-scoped business-column copy (the
// only place outside prompt_version.go allowed to write these columns).
func writeScopeEffectiveContentTx(ctx context.Context, qtx *db.Queries, scope promptVersionScope, scopeID pgtype.UUID, content string) error {
	var err error
	switch scope {
	case promptVersionScopeWorkspace:
		_, err = qtx.UpdateWorkspaceContextForPromptVersion(ctx, db.UpdateWorkspaceContextForPromptVersionParams{ID: scopeID, Context: strToText(content)})
	case promptVersionScopeProject:
		_, err = qtx.UpdateProjectInstructionsForPromptVersion(ctx, db.UpdateProjectInstructionsForPromptVersionParams{ID: scopeID, Instructions: strToText(content)})
	case promptVersionScopeSquad:
		_, err = qtx.UpdateSquadInstructionsForPromptVersion(ctx, db.UpdateSquadInstructionsForPromptVersionParams{ID: scopeID, Instructions: content})
	case promptVersionScopeAgent:
		_, err = qtx.UpdateAgentInstructionsForPromptVersion(ctx, db.UpdateAgentInstructionsForPromptVersionParams{ID: scopeID, Instructions: content})
	case promptVersionScopeAutopilot:
		_, err = qtx.UpdateAutopilotDescriptionForPromptVersion(ctx, db.UpdateAutopilotDescriptionForPromptVersionParams{ID: scopeID, Description: strToText(content)})
	case promptVersionScopeSkill:
		_, err = qtx.UpdateSkillContentForPromptVersion(ctx, db.UpdateSkillContentForPromptVersionParams{ID: scopeID, Content: content})
	}
	return err
}
