// RUYI-645: daemon-facing ops endpoints — command result receipts and the
// machine service inventory report — plus the heartbeat-ack claim hook.
// All routes here sit behind the DaemonAuth middleware; a receipt must echo
// the lease epoch the claim handed out, and the inventory POST is accepted
// only from a daemon that owns runtimes in the caller's workspace.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReportOpsCommandResult serves POST
// /api/daemon/runtimes/{runtimeId}/ops-commands/{commandId}/result. The
// receipt finalizes the command only when its (command, runtime, lease
// epoch) triple matches the current claim generation; a stale or misplaced
// receipt gets 409 plus an audit record and never overwrites terminal
// state (assertion 3).
func (h *Handler) ReportOpsCommandResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	if _, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID); !ok {
		return
	}

	commandID := chi.URLParam(r, "commandId")
	if _, ok := parseUUIDOrBadRequest(w, commandID, "command_id"); !ok {
		return
	}

	var receipt service.OpsReceipt
	if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	receipt.CommandID = commandID
	receipt.RuntimeID = runtimeID

	command, err := service.FinalizeOpsCommandReceipt(r.Context(), h.Queries, receipt)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOpsEpochMismatch):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrOpsResultTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		default:
			slog.Warn("ops command receipt rejected", "runtime_id", runtimeID, "command_id", commandID, "error", err)
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, opsCommandToResponse(runtimeID, command, false))
}

// ReportDaemonInventory serves POST /api/daemon/inventory (design §5).
// Body: {daemon_id, collect_status, services, error}. Accept-upsert keyed
// by daemon_id; the caller must authenticate as a daemon whose workspace
// already owns runtimes for that daemon_id, so a daemon token cannot write
// another machine's inventory.
func (h *Handler) ReportDaemonInventory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DaemonID      string                   `json:"daemon_id"`
		CollectStatus string                   `json:"collect_status"`
		Services      []daemonInventoryService `json:"services"`
		Error         string                   `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.DaemonID = trimSpace(body.DaemonID)
	if body.DaemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	if body.CollectStatus != "ok" && body.CollectStatus != "unavailable" {
		writeError(w, http.StatusBadRequest, "collect_status must be ok or unavailable")
		return
	}

	// Proof of ownership: the reporting daemon must own runtimes in the
	// authenticated caller's workspace(s). Daemon tokens prove a workspace
	// directly; PAT/JWT callers must be members of every workspace the
	// daemon registered into.
	workspaceIDs, err := h.Queries.ListDaemonInventoryWorkspaces(r.Context(), strToTextPtr(body.DaemonID))
	if err != nil || len(workspaceIDs) == 0 {
		writeError(w, http.StatusNotFound, "unknown daemon")
		return
	}
	for _, wsID := range workspaceIDs {
		if !h.verifyDaemonWorkspaceAccess(r, uuidToString(wsID)) {
			writeError(w, http.StatusForbidden, "daemon is not in your workspace")
			return
		}
	}

	if body.CollectStatus == "unavailable" {
		// A failed collection never carries a partial list: the services
		// array stays empty so the Web surface renders the unavailable form
		// instead of zero-filled rows (assertion 2).
		body.Services = []daemonInventoryService{}
	}

	servicesJSON, err := json.Marshal(body.Services)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid services payload")
		return
	}

	inv, err := h.Queries.UpsertDaemonServiceInventory(r.Context(), db.UpsertDaemonServiceInventoryParams{
		DaemonID:      body.DaemonID,
		CollectStatus: body.CollectStatus,
		Services:      servicesJSON,
		Error:         strToTextPtr(body.Error),
	})
	if err != nil {
		slog.Warn("daemon inventory upsert failed", "daemon_id", body.DaemonID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record inventory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "collect_status": inv.CollectStatus})
}

// claimOpsCommands runs inside processHeartbeat: atomically claim up to
// OpsCommandMaxPerBeat pending commands and attach them to the ack. The
// claim UPDATE locks rows FOR UPDATE SKIP LOCKED, so two server instances
// (or two heartbeats racing) can never hand out the same command.
// Best-effort: a failed claim costs one heartbeat of latency, never the
// heartbeat itself.
func (h *Handler) claimOpsCommands(ctx context.Context, rt db.AgentRuntime, ack *protocol.DaemonHeartbeatAckPayload) {
	if rt.Status != "online" {
		return
	}
	claimed, err := service.ClaimOpsCommands(ctx, h.Queries, rt.ID, service.OpsCommandMaxPerBeat)
	if err != nil {
		slog.Warn("ops command claim failed", "runtime_id", uuidToString(rt.ID), "error", err)
		return
	}
	if len(claimed) == 0 {
		return
	}
	dispatches := make([]protocol.DaemonOpsCommandDispatch, 0, len(claimed))
	for _, row := range claimed {
		dispatches = append(dispatches, protocol.DaemonOpsCommandDispatch{
			CommandID:  uuidToString(row.ID),
			Kind:       row.Kind,
			Args:       json.RawMessage(row.Args),
			TimeoutS:   int(row.TimeoutS),
			LeaseEpoch: row.LeaseEpoch,
		})
	}
	ack.PendingOpsCommands = dispatches
}

func (h *Handler) claimFileOperations(ctx context.Context, rt db.AgentRuntime, ack *protocol.DaemonHeartbeatAckPayload) {
	if rt.Status != "online" {
		return
	}
	if err := h.Queries.ExpireFileOperations(ctx); err != nil {
		slog.Warn("file operation expiration failed", "runtime_id", uuidToString(rt.ID), "error", err)
	}
	claimed, err := h.Queries.ClaimFileOperations(ctx, db.ClaimFileOperationsParams{RuntimeID: rt.ID, Limit: 3})
	if err != nil {
		slog.Warn("file operation claim failed", "runtime_id", uuidToString(rt.ID), "error", err)
		return
	}
	for _, row := range claimed {
		var req fileaccess.Request
		if json.Unmarshal(row.Request, &req) != nil {
			continue
		}
		var target db.DaemonFileAccessTarget
		target, err = h.Queries.GetFileAccessTargetByRuntime(ctx, db.GetFileAccessTargetByRuntimeParams{ID: row.TargetID, RuntimeID: rt.ID})
		if err != nil || !target.Enabled {
			continue
		}
		var roots []string
		if json.Unmarshal(target.Roots, &roots) != nil || len(roots) == 0 {
			continue
		}
		ack.PendingFileOperations = append(ack.PendingFileOperations, protocol.DaemonFileOperationDispatch{
			OperationID: uuidToString(row.ID), TargetID: row.TargetID, Kind: row.Kind,
			Path: req.Path, Offset: req.Offset, Length: req.Length, Limit: req.Limit,
			Cursor: req.Cursor, ExpectedSize: req.ExpectedSize, ExpectedModTime: formatTime(req.ExpectedModTime),
			LeaseEpoch: row.LeaseEpoch, Roots: roots,
		})
	}
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

func strToTextPtr(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// nonEmptyStrings returns the slice unchanged when it carries values and
// nil when empty, so an omitted JSON array stays absent in metadata rather
// than materializing as [].
func nonEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}
