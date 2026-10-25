package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ClauseMigrationRollbackItem names one migrated clause to revert: the asset
// to withdraw and the `## ` section its body must be restored into. The body
// itself always comes from the clause's latest clause_version — the asset is
// the single fact source, never the caller's copy.
type ClauseMigrationRollbackItem struct {
	ClauseID pgtype.UUID
	Section  string
}

// RollbackClauseMigrationBatch reverts one legacy-migration batch inside a
// single transaction (ADR 007 §8 彻底回退): every binding in the batch is
// withdrawn (clause deprecated), the rule text is restored from the asset's
// latest version into the stable context at the recorded section, and every
// affected carrier is re-rendered through the full governance version funnel.
// After a successful rollback the carrier's injection behavior is
// semantically back to pre-migration: no marker for the batch clauses, rule
// text back in the stable half, remaining clause segments untouched.
//
// Restored placement is deterministic (end of the recorded section, or the
// document tail when the section is empty) — the original line position is
// deliberately not preserved; what must hold is byte-identical rule text and
// a healthy reconciliation.
//
// The batch either fully rolls back or not at all; rolling back an
// already-withdrawn clause fails the whole call (fail-closed, idempotency by
// refusal).
func (h *Handler) RollbackClauseMigrationBatch(ctx context.Context, workspaceID pgtype.UUID, items []ClauseMigrationRollbackItem, actorType, actorID, changeNote string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, errors.New("回滚批次不能为空")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("回滚事务开启失败: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)

	// Pass 1 — withdraw every binding and deprecate every clause. Any item
	// without an active binding means the batch state is not what the caller
	// thinks; refuse instead of half-rolling-back.
	type reverted struct {
		binding db.ClauseBinding
		body    string
		section string
		title   string
	}
	revertedItems := make([]reverted, 0, len(items))
	scopes := map[string]bool{}
	for _, item := range items {
		clause, err := qtx.GetClauseByID(ctx, db.GetClauseByIDParams{ID: item.ClauseID, WorkspaceID: workspaceID})
		if err != nil {
			return nil, fmt.Errorf("回滚条款读取失败: %w", err)
		}
		binding, err := qtx.WithdrawClauseBinding(ctx, db.WithdrawClauseBindingParams{
			ClauseID: clause.ID,
			Audit:    clauseAudit("binding_withdrawn", actorType, actorID, map[string]any{"reason": "migration_rollback", "change_note": changeNote}),
		})
		if err != nil {
			return nil, fmt.Errorf("条款「%s」没有生效绑定，无法回滚（可能已回滚过）", clause.Title)
		}
		if _, err := qtx.UpdateClauseStatus(ctx, db.UpdateClauseStatusParams{
			ID: clause.ID, WorkspaceID: workspaceID, Status: "deprecated",
		}); err != nil {
			return nil, fmt.Errorf("条款停用失败: %w", err)
		}
		versions, err := qtx.ListClauseVersions(ctx, clause.ID)
		if err != nil {
			return nil, fmt.Errorf("条款「%s」版本读取失败: %w", clause.Title, err)
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("条款「%s」没有任何版本，无法还原正文", clause.Title)
		}
		latest := versions[len(versions)-1]
		revertedItems = append(revertedItems, reverted{
			binding: binding, body: latest.Content, section: item.Section, title: clause.Title,
		})
		scopes[binding.Scope+"/"+uuidToString(binding.ScopeID)] = true
	}

	// Pass 2 — lock every affected carrier in deterministic order (scope,
	// then scope id) so concurrent enact/rollback runs serialize the same
	// way every time.
	locks := make([]carrierRef, 0, len(scopes))
	for key := range scopes {
		scope, scopeID := splitCarrierKey(key)
		locks = append(locks, carrierRef{scope: scope, id: scopeID})
	}
	sort.Slice(locks, func(i, j int) bool {
		if locks[i].scope != locks[j].scope {
			return locks[i].scope < locks[j].scope
		}
		return uuidToString(locks[i].id) < uuidToString(locks[j].id)
	})
	texts := map[carrierRef]string{}
	for _, c := range locks {
		current, err := lockScopeEntityResult(ctx, qtx, promptVersionScope(c.scope), workspaceID, c.id)
		if err != nil {
			return nil, fmt.Errorf("载体加锁失败（%s）: %w", c.scope, err)
		}
		texts[c] = current
	}

	// Pass 3 — restore bodies into the stable context, then re-render the
	// carrier with the remaining active clause set through the version
	// funnel (fail-closed gate + version row + business column + baseline).
	allClauses, err := qtx.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("有效条款集读取失败: %w", err)
	}
	byCarrier := renderedClausesByCarrier(allClauses, nil)
	result := map[string]string{}
	for _, c := range locks {
		stable := clausemark.StripMarkers(texts[c])
		for _, r := range revertedItems {
			if r.binding.Scope != c.scope || r.binding.ScopeID != c.id {
				continue
			}
			stable, err = legislation.RestoreBlockToSection(stable, r.section, r.body)
			if err != nil {
				return nil, fmt.Errorf("条款「%s」还原失败: %w", r.title, err)
			}
		}
		rendered := clausemark.Render(stable, byCarrier[c])
		scan, ok, reason := promptGovernanceGate(rendered)
		if !ok {
			return nil, fmt.Errorf("回滚重渲染未通过内容门闸（%s）: %s", c.scope, reason)
		}
		if _, err := persistCarrierVersionTx(ctx, qtx, workspaceID, promptVersionScope(c.scope), c.id, rendered,
			changeNote, "legislation", nil, scan.Revision, scan.Findings, actorID); err != nil {
			return nil, fmt.Errorf("载体版本写入失败（%s）: %w", c.scope, err)
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
			return nil, fmt.Errorf("结构基线重建失败（%s）: %w", c.scope, err)
		}
		result[c.scope+"/"+uuidToString(c.id)] = sha256Hex(rendered)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("回滚事务提交失败: %w", err)
	}
	return result, nil
}

// splitCarrierKey splits "scope/uuid" back into its parts (scopes never
// contain '/').
func splitCarrierKey(key string) (string, pgtype.UUID) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], parseUUID(key[i+1:])
		}
	}
	return key, pgtype.UUID{}
}
