package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fileTargetBody struct {
	ID           string   `json:"id"`
	Enabled      bool     `json:"enabled"`
	Roots        []string `json:"roots"`
	WorkspaceIDs []string `json:"workspace_ids"`
	MemberIDs    []string `json:"member_ids"`
}

// PutRuntimeFileAccessTarget registers an explicit target/runtime binding.
// No target is created implicitly and roots are deny-by-default.
func (h *Handler) PutRuntimeFileAccessTarget(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}
	var body fileTargetBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" || len(body.ID) > 64 || strings.ContainsAny(body.ID, "/\\\x00") {
		writeError(w, http.StatusBadRequest, "invalid target id")
		return
	}
	if len(body.Roots) == 0 || len(body.Roots) > 32 {
		writeError(w, http.StatusBadRequest, "roots must contain 1 to 32 absolute paths")
		return
	}
	for _, memberID := range body.MemberIDs {
		if _, err := parseUUIDLoose(memberID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid member id")
			return
		}
	}
	for i, root := range body.Roots {
		clean, err := fileaccess.NormalizePathUnfiltered(root)
		if err != nil {
			writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
			return
		}
		body.Roots[i] = clean
	}
	workspaceID := uuidToString(rt.WorkspaceID)
	if len(body.WorkspaceIDs) == 0 {
		body.WorkspaceIDs = []string{workspaceID}
	}
	for _, id := range body.WorkspaceIDs {
		if id != workspaceID {
			writeError(w, http.StatusBadRequest, "target workspace grants must match the runtime workspace")
			return
		}
	}
	roots, _ := json.Marshal(body.Roots)
	workspaces, _ := json.Marshal(body.WorkspaceIDs)
	members, _ := json.Marshal(body.MemberIDs)
	target, err := h.Queries.UpsertFileAccessTarget(r.Context(), db.UpsertFileAccessTargetParams{
		ID: body.ID, WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID, Enabled: body.Enabled,
		Roots: roots, WorkspaceAcl: workspaces, MemberAcl: members,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "target id is already bound to another runtime")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to register file target")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID, "runtime_id": uuidToString(target.RuntimeID), "enabled": target.Enabled, "root_count": len(body.Roots)})
}

func (h *Handler) ListRuntimeFiles(w http.ResponseWriter, r *http.Request) {
	h.submitFileOperation(w, r, fileaccess.KindList)
}
func (h *Handler) GetRuntimeFileMetadata(w http.ResponseWriter, r *http.Request) {
	h.submitFileOperation(w, r, fileaccess.KindMetadata)
}
func (h *Handler) ReadRuntimeFileChunk(w http.ResponseWriter, r *http.Request) {
	h.submitFileOperation(w, r, fileaccess.KindRead)
}

func (h *Handler) submitFileOperation(w http.ResponseWriter, r *http.Request, kind fileaccess.Kind) {
	var req fileaccess.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Kind = kind
	if err := fileaccess.ValidateRequestStructure(req); err != nil {
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	targets, err := h.Queries.ListFileAccessTargetsByHost(r.Context(), req.TargetID)
	if err != nil {
		writeError(w, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
		return
	}
	target, err := resolveFileAccessTargetByHost(req.TargetID, targets)
	if err != nil {
		writeError(w, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
		return
	}
	runtimeID := uuidToString(target.RuntimeID)
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "runtime_file_access", runtimeID)
	if !ok {
		return
	}
	if routeRuntimeID := chi.URLParam(r, "runtimeId"); routeRuntimeID != "" && routeRuntimeID != runtimeID {
		writeError(w, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
		return
	}
	req.TargetID = target.ID
	var workspaceIDs, memberIDs []string
	if json.Unmarshal(target.WorkspaceAcl, &workspaceIDs) != nil || json.Unmarshal(target.MemberAcl, &memberIDs) != nil {
		writeError(w, http.StatusForbidden, fileaccess.ErrorPermissionDenied.Error())
		return
	}
	targetPolicy := fileaccess.Target{ID: target.ID, Enabled: target.Enabled, WorkspaceIDs: workspaceIDs, MemberIDs: memberIDs}
	if err := fileaccess.HasTargetAccess(targetPolicy, uuidToString(rt.WorkspaceID), uuidToString(member.UserID)); err != nil {
		h.auditFileAccessDenied(r.Context(), rt.WorkspaceID, rt.ID, member.UserID, target.ID, fileAccessErrorClass(err))
		writeError(w, http.StatusForbidden, fileAccessErrorClass(err))
		return
	}
	var roots []string
	if json.Unmarshal(target.Roots, &roots) != nil || len(roots) == 0 {
		writeError(w, http.StatusForbidden, fileaccess.ErrorPermissionDenied.Error())
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
	inFlight, err := h.Queries.CountInFlightFileOperations(r.Context(), rt.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check file operation quota")
		return
	}
	if inFlight >= 5 {
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	policy, err := h.loadMCPFileReadPolicy(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "file read policy unavailable")
		return
	}
	if err := fileaccess.ValidateRequestWithPolicy(req, policy); err != nil {
		h.auditFileAccessDenied(r.Context(), rt.WorkspaceID, rt.ID, member.UserID, target.ID, fileAccessErrorClass(err))
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	canonical, err := fileaccess.NormalizePathWithPolicy(req.Path, policy)
	if err != nil {
		h.auditFileAccessDenied(r.Context(), rt.WorkspaceID, rt.ID, member.UserID, target.ID, fileAccessErrorClass(err))
		writeError(w, http.StatusBadRequest, fileAccessErrorClass(err))
		return
	}
	if !fileaccess.PathWithinRootsWithPolicy(roots, canonical, policy) {
		h.auditFileAccessDenied(r.Context(), rt.WorkspaceID, rt.ID, member.UserID, target.ID, fileaccess.ErrorPathNotAllowed.Error())
		writeError(w, http.StatusForbidden, fileaccess.ErrorPathNotAllowed.Error())
		return
	}
	req.Path = canonical
	encoded, _ := json.Marshal(req)
	operation, err := h.Queries.InsertFileOperation(r.Context(), db.InsertFileOperationParams{
		ID: dbid.NewV7(), WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID,
		TargetID: target.ID, Kind: string(kind), Request: encoded, RequesterID: member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue file operation")
		return
	}
	h.requestDaemonPendingWork(runtimeID, protocol.PendingWorkKindFileAccess)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation_id": uuidToString(operation.ID), "status": operation.Status, "target_id": target.ID, "path": canonical})
}

func resolveFileAccessTargetByHost(host string, targets []db.DaemonFileAccessTarget) (db.DaemonFileAccessTarget, error) {
	registrations := make([]fileaccess.Target, 0, len(targets))
	for _, target := range targets {
		registrations = append(registrations, fileaccess.Target{ID: target.ID, RuntimeID: uuidToString(target.RuntimeID)})
	}
	targetID, err := fileaccess.ResolveTargetIDByHost(host, registrations)
	if err != nil {
		return db.DaemonFileAccessTarget{}, fileaccess.ErrorHostNotRegistered
	}
	for _, target := range targets {
		if target.ID == targetID {
			return target, nil
		}
	}
	return db.DaemonFileAccessTarget{}, fileaccess.ErrorHostNotRegistered
}

func (h *Handler) GetRuntimeFileOperation(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "runtime_file_access", runtimeID)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	op, err := h.Queries.GetFileOperation(r.Context(), id)
	if err != nil || op.RuntimeID != rt.ID {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	target, err := h.Queries.GetFileAccessTargetByRuntime(r.Context(), db.GetFileAccessTargetByRuntimeParams{ID: op.TargetID, RuntimeID: rt.ID})
	if err != nil {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	var workspaceIDs, memberIDs []string
	if json.Unmarshal(target.WorkspaceAcl, &workspaceIDs) != nil || json.Unmarshal(target.MemberAcl, &memberIDs) != nil || fileaccess.HasTargetAccess(fileaccess.Target{ID: target.ID, Enabled: target.Enabled, WorkspaceIDs: workspaceIDs, MemberIDs: memberIDs}, uuidToString(rt.WorkspaceID), uuidToString(member.UserID)) != nil {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	var result any
	if len(op.Result) > 0 {
		_ = json.Unmarshal(op.Result, &result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation_id": uuidToString(op.ID), "target_id": op.TargetID, "kind": op.Kind, "status": op.Status, "result": result, "error": textValue(op.ErrorClass)})
}

func (h *Handler) ReportFileOperationResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	if _, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID); !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	var receipt struct {
		Status        string          `json:"status"`
		LeaseEpoch    int64           `json:"lease_epoch"`
		PolicyVersion int64           `json:"policy_version"`
		Result        json.RawMessage `json:"result"`
		Error         string          `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&receipt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if receipt.Status != "succeeded" && receipt.Status != "rejected" && receipt.Status != "failed" {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if receipt.Status == "succeeded" && len(receipt.Result) == 0 {
		writeError(w, http.StatusBadRequest, "result is required")
		return
	}
	if receipt.PolicyVersion < 1 {
		writeError(w, http.StatusConflict, "file read policy is stale")
		return
	}
	if len(receipt.Result) > 96*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "result exceeds limit")
		return
	}
	var result []byte
	if receipt.Status == "succeeded" {
		result = receipt.Result
	}
	var errorClassValue = strings.TrimSpace(receipt.Error)
	if receipt.Status != "succeeded" && !validFileAccessErrorClass(errorClassValue) {
		writeError(w, http.StatusBadRequest, "invalid error class")
		return
	}
	if receipt.Status == "rejected" || receipt.Status == "failed" {
		result = nil
	}
	operation, err := h.Queries.FinalizeFileOperationForPolicy(r.Context(), db.FinalizeFileOperationForPolicyParams{
		ID: id, RuntimeID: parseUUID(runtimeID), Status: receipt.Status, Result: result,
		ErrorClass: strToTextPtr(errorClassValue), LeaseEpoch: receipt.LeaseEpoch, PolicyVersion: receipt.PolicyVersion,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "file operation lease is stale")
		return
	}
	if receipt.Status == "rejected" {
		h.auditFileAccessDenied(r.Context(), operation.WorkspaceID, operation.RuntimeID, operation.RequesterID, operation.TargetID, errorClassValue)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// auditFileAccessDenied stores only the server-resolved identities and reason.
// Paths and file content must never enter the audit trail.
func (h *Handler) auditFileAccessDenied(ctx context.Context, workspaceID, runtimeID, requesterID pgtype.UUID, targetID, errorClass string) {
	details, err := json.Marshal(map[string]string{"target_id": targetID, "error_class": errorClass})
	if err != nil {
		return
	}
	service.TryAppendAuditEvents(ctx, h.Queries, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      service.AuditDomainRuntime,
		EventType:   service.AuditRuntimeFileAccessDenied,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     requesterID,
		RuntimeID:   runtimeID,
		Details:     details,
	})
}

func fileAccessErrorClass(err error) string {
	for _, class := range []error{fileaccess.ErrorHostNotRegistered, fileaccess.ErrorTargetDisabled, fileaccess.ErrorTargetUnreachable, fileaccess.ErrorPermissionDenied, fileaccess.ErrorPathNotAllowed, fileaccess.ErrorSensitivePathDenied, fileaccess.ErrorSymlinkEscapeDenied, fileaccess.ErrorNotFound, fileaccess.ErrorNotRegularFile, fileaccess.ErrorRangeInvalid, fileaccess.ErrorFileChanged, fileaccess.ErrorFileTooLarge, fileaccess.ErrorUnsupportedResolution} {
		if errors.Is(err, class) {
			return class.Error()
		}
	}
	return "internal_error"
}

func validFileAccessErrorClass(value string) bool {
	for _, class := range []error{fileaccess.ErrorHostNotRegistered, fileaccess.ErrorTargetDisabled, fileaccess.ErrorTargetUnreachable, fileaccess.ErrorPermissionDenied, fileaccess.ErrorPathNotAllowed, fileaccess.ErrorSensitivePathDenied, fileaccess.ErrorSymlinkEscapeDenied, fileaccess.ErrorNotFound, fileaccess.ErrorNotRegularFile, fileaccess.ErrorRangeInvalid, fileaccess.ErrorFileChanged, fileaccess.ErrorFileTooLarge, fileaccess.ErrorUnsupportedResolution} {
		if value == class.Error() {
			return true
		}
	}
	return value == "timeout" || value == "rate_limited" || value == "internal_error"
}
