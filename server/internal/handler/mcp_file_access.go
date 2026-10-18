package handler

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const mcpFileAccessRequestLimit = 8 << 10

type mcpFileAccessRequest struct {
	InvocationID string `json:"invocation_id"`
	Host         string `json:"host,omitempty"`
	Path         string `json:"path"`
	Offset       int64  `json:"offset,omitempty"`
	Length       int64  `json:"length,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
}

type mcpFileAccessBindingRequest struct {
	TargetID string `json:"target_id"`
	Enabled  bool   `json:"enabled"`
}

func (h *Handler) requireMCPFileAccessInstance(w http.ResponseWriter, r *http.Request) (string, bool) {
	instanceID := strings.TrimSpace(r.Header.Get("X-Multica-MCP-Instance-ID"))
	provided := r.Header.Get("X-Multica-MCP-Instance-Credential")
	expected := h.cfg.MCPFileAccessInstanceCredentials[instanceID]
	if instanceID == "" || expected == "" || len(expected) != len(provided) ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid MCP instance credential")
		return "", false
	}
	return instanceID, true
}

func (h *Handler) PutMCPFileAccessInstanceBinding(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceId")
	if _, ok := h.cfg.MCPFileAccessInstanceCredentials[instanceID]; !ok {
		writeError(w, http.StatusNotFound, "MCP instance not found")
		return
	}
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}
	memberID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var input mcpFileAccessBindingRequest
	if !decodeMCPAuditRequest(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.TargetID) != input.TargetID || input.TargetID == "" {
		writeError(w, http.StatusBadRequest, "invalid binding request")
		return
	}
	row, err := h.Queries.UpsertMCPFileAccessInstanceBinding(r.Context(), db.UpsertMCPFileAccessInstanceBindingParams{
		InstanceID: instanceID, WorkspaceID: rt.WorkspaceID, TargetID: input.TargetID,
		Enabled: input.Enabled, UpdatedBy: parseUUID(memberID),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "file access target not found for runtime workspace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance_id": row.InstanceID, "workspace_id": uuidToString(row.WorkspaceID), "target_id": row.TargetID, "enabled": row.Enabled})
}

func (h *Handler) ListMCPFiles(w http.ResponseWriter, r *http.Request) {
	h.submitMCPFileOperation(w, r, fileaccess.KindList, "list_files")
}

func (h *Handler) GetMCPFileMetadata(w http.ResponseWriter, r *http.Request) {
	h.submitMCPFileOperation(w, r, fileaccess.KindMetadata, "get_file_metadata")
}

func (h *Handler) ReadMCPFileChunk(w http.ResponseWriter, r *http.Request) {
	h.submitMCPFileOperation(w, r, fileaccess.KindRead, "read_file_chunk")
}

func (h *Handler) submitMCPFileOperation(w http.ResponseWriter, r *http.Request, kind fileaccess.Kind, toolName string) {
	instanceID, ok := h.requireMCPFileAccessInstance(w, r)
	if !ok {
		return
	}
	var input mcpFileAccessRequest
	if !decodeMCPAuditRequest(w, r, &input) {
		return
	}
	invocationID, actorID, workspaceID, ok := h.authorizeMCPFileInvocation(w, r, input.InvocationID, instanceID, toolName)
	if !ok {
		return
	}
	req := fileaccess.Request{TargetID: input.Host, Kind: kind, Path: input.Path, Offset: input.Offset, Length: input.Length, Limit: input.Limit, Cursor: input.Cursor}
	var target db.DaemonFileAccessTarget
	var err error
	if input.Host == "" {
		target, err = h.Queries.GetMCPFileAccessInstanceBindingTarget(r.Context(), db.GetMCPFileAccessInstanceBindingTargetParams{InstanceID: instanceID, WorkspaceID: workspaceID})
	} else {
		var targets []db.DaemonFileAccessTarget
		targets, err = h.Queries.ListFileAccessTargetsByHost(r.Context(), input.Host)
		if err == nil {
			target, err = resolveFileAccessTargetByHost(input.Host, targets)
		}
	}
	if err != nil || target.ID == "" || uuidToString(target.WorkspaceID) != uuidToString(workspaceID) {
		writeError(w, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
		return
	}
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "runtime_file_access", uuidToString(target.RuntimeID))
	if !ok {
		return
	}
	if rt.WorkspaceID != workspaceID || uuidToString(member.UserID) != uuidToString(actorID) {
		writeError(w, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
		return
	}
	var workspaceIDs, memberIDs, roots []string
	if json.Unmarshal(target.WorkspaceAcl, &workspaceIDs) != nil || json.Unmarshal(target.MemberAcl, &memberIDs) != nil ||
		fileaccess.HasTargetAccess(fileaccess.Target{ID: target.ID, Enabled: target.Enabled, WorkspaceIDs: workspaceIDs, MemberIDs: memberIDs}, uuidToString(workspaceID), uuidToString(actorID)) != nil {
		writeError(w, http.StatusForbidden, fileaccess.ErrorPermissionDenied.Error())
		return
	}
	if json.Unmarshal(target.Roots, &roots) != nil || len(roots) == 0 {
		writeError(w, http.StatusForbidden, fileaccess.ErrorPermissionDenied.Error())
		return
	}
	req.TargetID = target.ID
	if err := fileaccess.ValidateRequestStructure(req); err != nil {
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	policy, err := h.loadMCPFileReadPolicy(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "file read policy unavailable")
		return
	}
	if err := fileaccess.ValidateRequestWithPolicy(req, policy); err != nil {
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	canonical, err := fileaccess.NormalizePathWithPolicy(req.Path, policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	if !fileaccess.PathWithinRootsWithPolicy(roots, canonical, policy) {
		writeError(w, http.StatusForbidden, fileaccess.ErrorPathNotAllowed.Error())
		return
	}
	if rt.Status != "online" {
		writeError(w, http.StatusConflict, fileaccess.ErrorTargetUnreachable.Error())
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityFileReadPolicyV1) {
		writeError(w, http.StatusConflict, "daemon_upgrade_required")
		return
	}
	req.Path = canonical
	encoded, _ := json.Marshal(req)
	op, err := h.Queries.InsertFileOperation(r.Context(), db.InsertFileOperationParams{
		ID: dbid.NewV7(), WorkspaceID: workspaceID, RuntimeID: rt.ID, TargetID: target.ID,
		Kind: string(kind), Request: encoded, InvocationID: invocationID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue file operation")
		return
	}
	h.requestDaemonPendingWork(uuidToString(rt.ID), string(protocol.PendingWorkKindFileAccess))
	writeJSON(w, http.StatusAccepted, map[string]any{"operation_id": uuidToString(op.ID), "status": op.Status})
}

func (h *Handler) GetMCPFileOperation(w http.ResponseWriter, r *http.Request) {
	instanceID, ok := h.requireMCPFileAccessInstance(w, r)
	if !ok {
		return
	}
	invocationIDText := strings.TrimSpace(r.URL.Query().Get("invocation_id"))
	invocationID, err := uuid.Parse(invocationIDText)
	if err != nil || invocationIDText == "" {
		writeError(w, http.StatusBadRequest, "invalid invocation_id")
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "workspace not found"); !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	start, err := h.Queries.GetMCPInvocationAuditStart(r.Context(), pgtype.UUID{Bytes: invocationID, Valid: true})
	if err != nil || uuidToString(start.WorkspaceID) != uuidToString(workspaceID) || !h.mcpStartMatches(start, actorID, instanceID, "") {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	op, err := h.Queries.GetFileOperationForInvocation(r.Context(), db.GetFileOperationForInvocationParams{ID: operationID, InvocationID: pgtype.UUID{Bytes: invocationID, Valid: true}})
	if err != nil || op.WorkspaceID != workspaceID {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "runtime_file_access", uuidToString(op.RuntimeID))
	if !ok {
		return
	}
	if rt.WorkspaceID != workspaceID || uuidToString(member.UserID) != actorID {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	target, err := h.Queries.GetFileAccessTargetByRuntime(r.Context(), db.GetFileAccessTargetByRuntimeParams{ID: op.TargetID, RuntimeID: rt.ID})
	if err != nil {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	var workspaceIDs, memberIDs []string
	if json.Unmarshal(target.WorkspaceAcl, &workspaceIDs) != nil || json.Unmarshal(target.MemberAcl, &memberIDs) != nil ||
		fileaccess.HasTargetAccess(fileaccess.Target{ID: target.ID, Enabled: target.Enabled, WorkspaceIDs: workspaceIDs, MemberIDs: memberIDs}, uuidToString(workspaceID), actorID) != nil {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	var result any
	if len(op.Result) > 0 {
		result = projectMCPFileResult(op.Kind, op.Result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation_id": uuidToString(op.ID), "kind": op.Kind, "status": op.Status, "result": result, "error": mcpFileErrorCode(op.ErrorClass.String)})
}

func (h *Handler) authorizeMCPFileInvocation(w http.ResponseWriter, r *http.Request, invocationIDText, instanceID, toolName string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	invocationID, err := uuid.Parse(strings.TrimSpace(invocationIDText))
	if err != nil || strings.TrimSpace(invocationIDText) != invocationIDText {
		writeError(w, http.StatusBadRequest, "invalid invocation_id")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "workspace not found"); !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	start, err := h.Queries.GetMCPInvocationAuditStart(r.Context(), pgtype.UUID{Bytes: invocationID, Valid: true})
	if err != nil || uuidToString(start.WorkspaceID) != uuidToString(workspaceID) || !h.mcpStartMatches(start, actorID, instanceID, toolName) {
		writeError(w, http.StatusForbidden, "MCP invocation is not authorized for this file operation")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: invocationID, Valid: true}, start.ActorID, start.WorkspaceID, true
}

func (h *Handler) mcpStartMatches(start db.GetMCPInvocationAuditStartRow, actorID, instanceID, toolName string) bool {
	if !start.ActorID.Valid || uuidToString(start.ActorID) != actorID {
		return false
	}
	var details map[string]any
	if json.Unmarshal(start.Details, &details) != nil {
		return false
	}
	return details["instance_id"] == instanceID && (toolName == "" || details["tool_name"] == toolName)
}

func projectMCPFileResult(kind string, raw []byte) any {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	var output map[string]json.RawMessage
	keys := map[string][]string{
		string(fileaccess.KindList):     {"target_id", "path", "entries", "next_cursor"},
		string(fileaccess.KindMetadata): {"target_id", "path", "size", "mtime", "content_type"},
		string(fileaccess.KindRead):     {"target_id", "path", "offset", "length", "eof", "encoding", "data"},
	}[kind]
	if keys == nil {
		return nil
	}
	output = make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		if value, ok := input[key]; ok {
			output[key] = value
		}
	}
	return output
}

func mcpFileErrorCode(value string) string {
	switch value {
	case "path_not_allowed", "sensitive_path_denied", "symlink_escape_denied", "not_regular_file", "range_invalid", "file_changed", "file_too_large", "unsupported_path_resolution":
		return "invalid_input"
	case "permission_denied", "target_disabled":
		return "permission_denied"
	case "host_not_registered", "not_found":
		return "not_found"
	case "target_unreachable", "rate_limited":
		return "unavailable"
	case "timeout":
		return "timeout"
	case "":
		return ""
	default:
		return "internal"
	}
}
