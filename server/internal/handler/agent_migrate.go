package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// maxMigrateAgentsBatch bounds one migration request. The list page can select
// every agent in a workspace, so the bound exists to keep one request from
// row-locking an unbounded set for the length of a transaction — not because
// any real workspace approaches it.
const maxMigrateAgentsBatch = 200

// Skip reasons returned per agent. The caller renders them; they are never a
// request failure, which is what makes "select everything, migrate what you
// may" a usable bulk gesture.
const (
	migrateSkipForbidden       = "forbidden"
	migrateSkipNotFound        = "not_found"
	migrateSkipAlreadyOnTarget = "already_on_target"
)

// migrateAgentsToRuntimeRequest is the wire shape for
// POST /api/runtimes/{runtimeId}/migrate-agents. The runtime in the path is the
// TARGET; agent_ids are the agents to move onto it.
//
// expected_source_runtime_id is the stale-plan guard for the Runtime detail
// entry point ("migrate this runtime's agents"), where the user confirms a set
// the page rendered earlier. When present, the server re-derives that runtime's
// active agent set inside the transaction and refuses with
// runtime_migration_plan_changed if it no longer matches the request — the same
// contract UnbindAgentsAndDeleteRuntime uses for its cascade plan. The Agent
// List entry points omit it: there the user picked explicit rows, so the
// request itself is the confirmed set.
type migrateAgentsToRuntimeRequest struct {
	AgentIDs []string `json:"agent_ids"`
	// Optional. Empty means "no source snapshot to validate".
	ExpectedSourceRuntimeID string `json:"expected_source_runtime_id"`
	// Defaults to true when omitted: clearing the runtime-native model
	// settings is what a runtime switch has always done on the single-agent
	// path, and the confirmation dialog lists exactly what will be cleared.
	ClearModelSettings *bool `json:"clear_model_settings"`
	// When true, run every read, permission check and stale-plan check but
	// write nothing. Backs the confirmation dialog's exact task split.
	DryRun bool `json:"dry_run"`
}

type migratedAgentResult struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	// The values this migration cleared (or would clear, on a dry run).
	// Empty string means the agent had nothing set for that field.
	ClearedModel         string `json:"cleared_model,omitempty"`
	ClearedThinkingLevel string `json:"cleared_thinking_level,omitempty"`
	ClearedServiceTier   string `json:"cleared_service_tier,omitempty"`
}

type skippedAgentResult struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name,omitempty"`
	Reason  string `json:"reason"`
}

// migrateAgentsToRuntimeResponse is identical for dry runs and real runs so the
// dialog renders one shape: on a dry run the counts are the projection, on a
// real run they are what happened.
type migrateAgentsToRuntimeResponse struct {
	TargetRuntimeID string                `json:"target_runtime_id"`
	DryRun          bool                  `json:"dry_run"`
	Migrated        []migratedAgentResult `json:"migrated"`
	Skipped         []skippedAgentResult  `json:"skipped"`
	// Tasks moved onto the target runtime ('queued' / 'deferred' — nothing
	// owns them yet).
	TasksMigrated int64 `json:"tasks_migrated"`
	// Tasks left on their current runtime because a daemon already claimed
	// them ('dispatched' / 'running' / 'waiting_local_directory').
	TasksStayingActive int64 `json:"tasks_staying_active"`
}

// MigrateAgentsToRuntime re-binds one or more agents onto the runtime named in
// the path and moves their unclaimed tasks along with them.
//
// One handler serves every entry point — the Agent List row menu (one agent),
// the Agent List batch toolbar (N agents), the Runtime detail page (that
// runtime's agents) and the agent detail inspector's runtime field. A single
// agent is just N=1; there is no second code path whose behaviour could drift
// from the bulk one.
//
// Failure model, decided with the issue's reviewers (MUL-5758):
//   - Agents the caller may not manage, agents outside this workspace and
//     agents already on the target are reported in `skipped`. They are not
//     errors and do not roll anything back.
//   - Anything else is all-or-nothing: the whole transaction rolls back, so a
//     partially migrated selection can never be observed.
//
// The task move is the load-bearing half. Daemons list claim candidates by
// agent_task_queue.runtime_id, so before this endpoint existed a runtime switch
// left already-queued work visible only to the runtime the agent just left —
// permanently stranded when that machine was the failing one being evacuated.
func (h *Handler) MigrateAgentsToRuntime(w http.ResponseWriter, r *http.Request) {
	targetUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime_id")
	if !ok {
		return
	}

	var req migrateAgentsToRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentIDs, ok := parseBulkAgentIDs(w, req.AgentIDs, maxMigrateAgentsBatch)
	if !ok {
		return
	}

	target, err := h.Queries.GetAgentRuntime(r.Context(), targetUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	workspaceID := uuidToString(target.WorkspaceID)
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "runtime not found")
	if !ok {
		return
	}
	// Same gate UpdateAgent applies to a single runtime change: a private
	// runtime only accepts agents from its owner or a workspace admin.
	if !canUseRuntimeForAgent(member, target) {
		writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can move agents onto it")
		return
	}

	var expectedSource pgtype.UUID
	if req.ExpectedSourceRuntimeID != "" {
		expectedSource, ok = parseUUIDOrBadRequest(w, req.ExpectedSourceRuntimeID, "expected_source_runtime_id")
		if !ok {
			return
		}
	}

	clearModelSettings := true
	if req.ClearModelSettings != nil {
		clearModelSettings = *req.ClearModelSettings
	}

	if req.DryRun {
		h.previewAgentMigration(w, r, target, member, agentIDs, clearModelSettings)
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Lock the runtime rows this migration touches before reading anything.
	// Locking the target blocks a concurrent runtime delete from tearing it
	// down while we bind agents onto it; locking the source keeps its agent
	// set stable for the stale-plan comparison below. Sorted order so two
	// migrations in opposite directions cannot deadlock on the pair.
	lockIDs := []pgtype.UUID{target.ID}
	if expectedSource.Valid && uuidToString(expectedSource) != uuidToString(target.ID) {
		lockIDs = append(lockIDs, expectedSource)
	}
	if err := lockRuntimesInIDOrder(r.Context(), qtx, lockIDs); err != nil {
		slog.Warn("migrate agents: lock runtimes failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(target.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to lock runtime")
		return
	}

	if expectedSource.Valid {
		// The Runtime detail entry point confirmed a set the page rendered
		// before this request. Re-derive it under the lock and refuse if it
		// moved, so the user never migrates a plan they did not see.
		current, err := qtx.ListActiveAgentsByRuntimeForUpdate(r.Context(), expectedSource)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate the source runtime's agents")
			return
		}
		if !activeAgentSetMatches(current, uuidSetOf(agentIDs)) {
			resp := make([]AgentResponse, len(current))
			for i, a := range current {
				resp[i] = h.agentToResponse(a)
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":         "the agent set on this runtime changed; please review and confirm again.",
				"code":          "runtime_migration_plan_changed",
				"active_agents": resp,
			})
			return
		}
	}

	agents, err := qtx.ListAgentsByIDsForWorkspaceForUpdate(r.Context(), db.ListAgentsByIDsForWorkspaceForUpdateParams{
		AgentIds:    agentIDs,
		WorkspaceID: target.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agents")
		return
	}

	plan := planAgentMigration(agents, agentIDs, member, target.ID, clearModelSettings)
	out := migrateAgentsToRuntimeResponse{
		TargetRuntimeID: uuidToString(target.ID),
		Migrated:        plan.migrated,
		Skipped:         plan.skipped,
	}
	if len(plan.eligibleIDs) == 0 {
		// Nothing left to do once skips are accounted for. Still a success:
		// the caller asked for a state that already holds.
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Counted before the writes: afterwards the unclaimed rows carry the
	// target runtime and the split would report zero to move.
	counts, err := qtx.CountAgentTasksByMigrationGroup(r.Context(), plan.eligibleIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect agent tasks")
		return
	}
	out.TasksStayingActive = counts.ActiveCount

	migrated, err := qtx.MigrateAgentsToRuntime(r.Context(), db.MigrateAgentsToRuntimeParams{
		RuntimeID:          target.ID,
		RuntimeMode:        target.RuntimeMode,
		ClearModelSettings: clearModelSettings,
		AgentIds:           plan.eligibleIDs,
	})
	if err != nil {
		slog.Warn("migrate agents: update agents failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(target.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to migrate agents")
		return
	}

	repointed, err := qtx.RepointUnclaimedTasksToRuntime(r.Context(), db.RepointUnclaimedTasksToRuntimeParams{
		ToRuntimeID: target.ID,
		AgentIds:    plan.eligibleIDs,
	})
	if err != nil {
		slog.Warn("migrate agents: repoint queued tasks failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(target.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to move queued tasks onto the new runtime")
		return
	}
	out.TasksMigrated = int64(len(repointed))

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Post-commit fan-out, in the order publishRuntimeTeardown uses: tell
	// subscribers each agent changed, then wake the runtime that just
	// inherited claimable work.
	userID := uuidToString(member.UserID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	for _, a := range migrated {
		resp := h.agentToResponse(a)
		h.publish(protocol.EventAgentStatus, workspaceID, actorType, actorID, map[string]any{
			"agent": broadcastAgentResponse(resp),
		})
	}
	if len(repointed) > 0 && h.TaskService != nil {
		// Without this the target runtime can be holding a cached "no work
		// here" verdict from before the migration and would not claim the
		// tasks it just inherited until that cache expired.
		h.TaskService.NotifyRuntimeMayHaveWork(target.ID)
	}

	slog.Info("agents migrated to runtime",
		append(logger.RequestAttrs(r),
			"runtime_id", uuidToString(target.ID),
			"workspace_id", workspaceID,
			"migrated", len(migrated),
			"skipped", len(plan.skipped),
			"tasks_migrated", len(repointed),
			"migrated_by", userID)...)

	writeJSON(w, http.StatusOK, out)
}

// previewAgentMigration answers a dry run: every read, permission decision and
// task count of the real path, no writes and no row locks.
//
// The dialog needs this because no client-side projection can produce the task
// split. derive-presence's queuedCount merges 'dispatched' and
// 'waiting_local_directory' into "queued" and ignores 'deferred' entirely,
// which is neither the set that moves nor the set that stays.
func (h *Handler) previewAgentMigration(
	w http.ResponseWriter,
	r *http.Request,
	target db.AgentRuntime,
	member db.Member,
	agentIDs []pgtype.UUID,
	clearModelSettings bool,
) {
	agents, err := h.Queries.ListAgentsByIDsForWorkspace(r.Context(), db.ListAgentsByIDsForWorkspaceParams{
		AgentIds:    agentIDs,
		WorkspaceID: target.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agents")
		return
	}

	plan := planAgentMigration(agents, agentIDs, member, target.ID, clearModelSettings)
	out := migrateAgentsToRuntimeResponse{
		TargetRuntimeID: uuidToString(target.ID),
		DryRun:          true,
		Migrated:        plan.migrated,
		Skipped:         plan.skipped,
	}
	if len(plan.eligibleIDs) > 0 {
		counts, err := h.Queries.CountAgentTasksByMigrationGroup(r.Context(), plan.eligibleIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to inspect agent tasks")
			return
		}
		out.TasksMigrated = counts.UnclaimedCount
		out.TasksStayingActive = counts.ActiveCount
	}
	writeJSON(w, http.StatusOK, out)
}

// agentMigrationPlan is the classification shared by the dry run and the real
// run, so a preview can never disagree with what the write path then does.
type agentMigrationPlan struct {
	eligibleIDs []pgtype.UUID
	migrated    []migratedAgentResult
	skipped     []skippedAgentResult
}

// planAgentMigration splits the requested ids into "will move" and "skipped".
//
// Requested ids that came back from the workspace-scoped query are classified
// on their row; ids that did not come back are reported as not_found without
// any further probing — an agent in another workspace and an id that never
// existed are deliberately indistinguishable to the caller.
func planAgentMigration(
	agents []db.Agent,
	requested []pgtype.UUID,
	member db.Member,
	targetRuntimeID pgtype.UUID,
	clearModelSettings bool,
) agentMigrationPlan {
	byID := make(map[string]db.Agent, len(agents))
	for _, a := range agents {
		byID[uuidToString(a.ID)] = a
	}

	plan := agentMigrationPlan{
		migrated: []migratedAgentResult{},
		skipped:  []skippedAgentResult{},
	}
	targetKey := uuidToString(targetRuntimeID)
	for _, id := range requested {
		key := uuidToString(id)
		agent, found := byID[key]
		if !found {
			plan.skipped = append(plan.skipped, skippedAgentResult{AgentID: key, Reason: migrateSkipNotFound})
			continue
		}
		if !canManageAgentForMember(agent, member) {
			plan.skipped = append(plan.skipped, skippedAgentResult{
				AgentID: key,
				Name:    agent.Name,
				Reason:  migrateSkipForbidden,
			})
			continue
		}
		if agent.RuntimeID.Valid && uuidToString(agent.RuntimeID) == targetKey {
			plan.skipped = append(plan.skipped, skippedAgentResult{
				AgentID: key,
				Name:    agent.Name,
				Reason:  migrateSkipAlreadyOnTarget,
			})
			continue
		}
		result := migratedAgentResult{AgentID: key, Name: agent.Name}
		if clearModelSettings {
			// Reported so the dialog can name what it is about to discard
			// instead of clearing model settings silently.
			result.ClearedModel = agent.Model.String
			result.ClearedThinkingLevel = agent.ThinkingLevel.String
			result.ClearedServiceTier = agent.ServiceTier.String
		}
		plan.migrated = append(plan.migrated, result)
		plan.eligibleIDs = append(plan.eligibleIDs, agent.ID)
	}
	return plan
}

// parseBulkAgentIDs validates and de-duplicates the agent id list every bulk
// endpoint takes. Order is preserved so responses list agents the way the
// caller sent them.
func parseBulkAgentIDs(w http.ResponseWriter, raw []string, limit int) ([]pgtype.UUID, bool) {
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "agent_ids must not be empty")
		return nil, false
	}
	if len(raw) > limit {
		writeError(w, http.StatusBadRequest, "too many agents in one request")
		return nil, false
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]pgtype.UUID, 0, len(raw))
	for _, s := range raw {
		parsed, ok := parseUUIDOrBadRequest(w, s, "agent_ids")
		if !ok {
			return nil, false
		}
		key := uuidToString(parsed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, parsed)
	}
	return out, true
}

func uuidSetOf(ids []pgtype.UUID) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[uuidToString(id)] = struct{}{}
	}
	return out
}

// lockRuntimesInIDOrder takes the runtime row locks a migration needs. Sorting
// by id gives every caller the same acquisition order, so concurrent
// migrations between the same two runtimes queue instead of deadlocking.
func lockRuntimesInIDOrder(ctx context.Context, qtx *db.Queries, ids []pgtype.UUID) error {
	sorted := make([]pgtype.UUID, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool {
		return uuidToString(sorted[i]) < uuidToString(sorted[j])
	})
	for _, id := range sorted {
		if _, err := qtx.LockAgentRuntime(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
