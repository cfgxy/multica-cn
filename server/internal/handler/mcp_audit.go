package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
)

const mcpAuditRequestLimit = 8 << 10

type mcpAuditStartRequest struct {
	InvocationID          string `json:"invocation_id"`
	WorkspaceID           string `json:"workspace_id"`
	ToolName              string `json:"tool_name"`
	ReportedClientName    string `json:"reported_client_name,omitempty"`
	ReportedClientVersion string `json:"reported_client_version,omitempty"`
	InstanceID            string `json:"instance_id,omitempty"`
	TargetType            string `json:"target_type,omitempty"`
	TargetID              string `json:"target_id,omitempty"`
	NormalizedPath        string `json:"normalized_path,omitempty"`
}

type mcpAuditFinishRequest struct {
	InvocationID string `json:"invocation_id"`
	OperationID  string `json:"operation_id,omitempty"`
	Status       string `json:"status"`
	DurationMS   int64  `json:"duration_ms"`
	ErrorCode    string `json:"error_code,omitempty"`
}

type mcpAuditWriteResponse struct {
	Status string `json:"status"`
}

func (h *Handler) requireMCPAuditService(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(h.cfg.MCPAuditServiceCredential)
	provided := r.Header.Get("X-Multica-MCP-Service-Credential")
	if expected == "" || provided == "" || len(expected) != len(provided) ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid MCP service credential")
		return false
	}
	return true
}

func decodeMCPAuditRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, mcpAuditRequestLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid audit request")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid audit request")
		return false
	}
	return true
}

func (h *Handler) StartMCPInvocationAudit(w http.ResponseWriter, r *http.Request) {
	if !h.requireMCPAuditService(w, r) {
		return
	}
	var input mcpAuditStartRequest
	if !decodeMCPAuditRequest(w, r, &input) {
		return
	}
	invocationID, err := uuid.Parse(input.InvocationID)
	if err != nil || strings.TrimSpace(input.InvocationID) != input.InvocationID {
		writeError(w, http.StatusBadRequest, "invalid invocation_id")
		return
	}
	workspaceID, err := util.ParseUUID(input.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}
	if len(input.ToolName) == 0 || len(input.ToolName) > 64 || !isMCPToolName(input.ToolName) {
		writeError(w, http.StatusBadRequest, "invalid tool_name")
		return
	}
	if input.InstanceID != "" {
		if !isMCPFileToolName(input.ToolName) || input.TargetType != "" || input.TargetID != "" || input.NormalizedPath != "" {
			writeError(w, http.StatusBadRequest, "invalid MCP file invocation start")
			return
		}
		instanceID, ok := h.requireMCPFileAccessInstance(w, r)
		if !ok {
			return
		}
		if instanceID != input.InstanceID {
			writeError(w, http.StatusForbidden, "MCP instance mismatch")
			return
		}
	} else if isMCPFileToolName(input.ToolName) {
		writeError(w, http.StatusBadRequest, "MCP file invocation requires an instance identity")
		return
	}
	if len(input.TargetType) > 32 || len(input.TargetID) > 128 || strings.ContainsAny(input.TargetType+input.TargetID, "\r\n\x00") {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	if input.TargetType != "" && input.TargetType != "workspace" && input.TargetType != "issue" && input.TargetType != "host" {
		writeError(w, http.StatusBadRequest, "invalid target_type")
		return
	}
	if len(input.NormalizedPath) > 2048 || strings.ContainsAny(input.NormalizedPath, "\r\n\x00") ||
		(input.NormalizedPath != "" && (!path.IsAbs(input.NormalizedPath) || path.Clean(input.NormalizedPath) != input.NormalizedPath)) {
		writeError(w, http.StatusBadRequest, "invalid normalized_path")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "workspace not found"); !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	parsedActor, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid authenticated actor")
		return
	}
	details := map[string]any{"tool_name": input.ToolName}
	if name := sanitizeMCPReportedClientValue(input.ReportedClientName, 128); name != "" {
		details["reported_client_name"] = name
	}
	if version := sanitizeMCPReportedClientValue(input.ReportedClientVersion, 128); version != "" {
		details["reported_client_version"] = version
	}
	if input.InstanceID != "" {
		details["instance_id"] = input.InstanceID
	}
	if input.TargetType != "" {
		details["target_type"] = input.TargetType
	}
	if input.TargetID != "" {
		details["target_id"] = input.TargetID
	}
	if input.NormalizedPath != "" {
		details["normalized_path"] = input.NormalizedPath
	}
	event := service.MCPInvocationAuditEvent(service.MCPInvocationAuditContext{
		InvocationID: pgtype.UUID{Bytes: invocationID, Valid: true},
		WorkspaceID:  workspaceID,
		ActorType:    service.AuditActorMember,
		ActorID:      parsedActor,
	}, service.AuditMCPInvocationStarted, details, input.InstanceID != "" || input.NormalizedPath != "" || input.TargetType == "host")
	inserted, err := service.AppendMCPInvocationAuditEvent(r.Context(), h.Queries, event)
	if err != nil {
		logMCPAuditWriteFailure("start", err)
		writeError(w, http.StatusServiceUnavailable, "MCP audit storage unavailable")
		return
	}
	if !inserted {
		writeJSON(w, http.StatusConflict, mcpAuditWriteResponse{Status: "already_recorded"})
		return
	}
	writeJSON(w, http.StatusCreated, mcpAuditWriteResponse{Status: "recorded"})
}

func logMCPAuditWriteFailure(phase string, err error) {
	attrs := []any{"phase", phase, "error_code", "audit_write_failed"}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		attrs = append(attrs, "sqlstate", pgErr.Code, "constraint", pgErr.ConstraintName)
	}
	slog.Error("MCP invocation audit write failed", attrs...)
}

func (h *Handler) FinishMCPInvocationAudit(w http.ResponseWriter, r *http.Request) {
	if !h.requireMCPAuditService(w, r) {
		return
	}
	var input mcpAuditFinishRequest
	if !decodeMCPAuditRequest(w, r, &input) {
		return
	}
	invocationID, err := uuid.Parse(input.InvocationID)
	if err != nil || strings.TrimSpace(input.InvocationID) != input.InvocationID {
		writeError(w, http.StatusBadRequest, "invalid invocation_id")
		return
	}
	if input.Status != "success" && input.Status != "failure" && input.Status != "refused" {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if input.DurationMS < 0 || input.DurationMS > int64((24*time.Hour)/time.Millisecond) {
		writeError(w, http.StatusBadRequest, "invalid duration_ms")
		return
	}
	if input.ErrorCode != "" && input.ErrorCode != "invalid_input" && input.ErrorCode != "not_found" &&
		input.ErrorCode != "permission_denied" && input.ErrorCode != "unavailable" && input.ErrorCode != "timeout" &&
		input.ErrorCode != "internal" {
		writeError(w, http.StatusBadRequest, "invalid error_code")
		return
	}
	operationID := pgtype.UUID{}
	if input.OperationID != "" {
		parsed, err := uuid.Parse(input.OperationID)
		if err != nil || strings.TrimSpace(input.OperationID) != input.OperationID {
			writeError(w, http.StatusBadRequest, "invalid operation_id")
			return
		}
		operationID = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	parsedActor, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid authenticated actor")
		return
	}
	start, err := h.Queries.GetMCPInvocationAuditStart(r.Context(), pgtype.UUID{Bytes: invocationID, Valid: true})
	if err != nil {
		writeError(w, http.StatusNotFound, "MCP invocation start not found")
		return
	}
	if !start.ActorID.Valid || start.ActorID.Bytes != parsedActor.Bytes {
		writeError(w, http.StatusForbidden, "MCP invocation actor mismatch")
		return
	}
	var startDetails map[string]any
	if err := json.Unmarshal(start.Details, &startDetails); err != nil {
		writeError(w, http.StatusBadRequest, "invalid MCP invocation start")
		return
	}
	fileInvocation := false
	instanceID, _ := startDetails["instance_id"].(string)
	toolName, _ := startDetails["tool_name"].(string)
	if instanceID != "" {
		fileInvocation = true
		providedInstanceID, ok := h.requireMCPFileAccessInstance(w, r)
		if !ok {
			return
		}
		if instanceID != providedInstanceID || !isMCPFileToolName(toolName) {
			writeError(w, http.StatusForbidden, "MCP instance mismatch")
			return
		}
	} else if operationID.Valid {
		writeError(w, http.StatusBadRequest, "operation_id is only valid for a file invocation")
		return
	}
	if fileInvocation && input.Status == "success" && !operationID.Valid {
		writeError(w, http.StatusBadRequest, "successful file invocation requires operation_id")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(start.WorkspaceID), "workspace not found"); !ok {
		return
	}
	details := map[string]any{"status": input.Status, "duration_ms": input.DurationMS}
	if fileInvocation {
		details["instance_id"] = instanceID
		if operationID.Valid {
			op, err := h.Queries.GetFileOperationForInvocation(r.Context(), db.GetFileOperationForInvocationParams{ID: operationID, InvocationID: start.InvocationID})
			if err != nil || uuidToString(op.WorkspaceID) != uuidToString(start.WorkspaceID) {
				writeError(w, http.StatusNotFound, "file operation not found")
				return
			}
			expectedKind := map[string]string{"list_files": "file_list", "get_file_metadata": "file_metadata", "read_file_chunk": "file_read_chunk"}[toolName]
			if op.Kind != expectedKind {
				writeError(w, http.StatusForbidden, "file operation type mismatch")
				return
			}
			var stored fileaccess.Request
			if json.Unmarshal(op.Request, &stored) != nil || stored.Path == "" {
				writeError(w, http.StatusInternalServerError, "invalid stored file operation")
				return
			}
			details["operation_id"] = input.OperationID
			details["operation_status"] = op.Status
			details["target_id"] = op.TargetID
			details["normalized_path"] = stored.Path
		}
	}
	if input.ErrorCode != "" {
		details["error_code"] = input.ErrorCode
	}
	event := service.MCPInvocationAuditEvent(service.MCPInvocationAuditContext{
		InvocationID: start.InvocationID,
		WorkspaceID:  start.WorkspaceID,
		ActorType:    service.AuditActorMember,
		ActorID:      start.ActorID,
	}, service.AuditMCPInvocationFinished, details, start.Sensitive)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	status := service.FinishMCPInvocationAudit(finishCtx, h.Queries, event)
	if status == "unrecorded" {
		writeJSON(w, http.StatusServiceUnavailable, mcpAuditWriteResponse{Status: status})
		return
	}
	writeJSON(w, http.StatusOK, mcpAuditWriteResponse{Status: status})
}

func isMCPToolName(value string) bool {
	for i, ch := range value {
		if ch >= 'a' && ch <= 'z' || i > 0 && ch >= '0' && ch <= '9' || i > 0 && ch == '_' {
			continue
		}
		return false
	}
	return true
}

func isMCPFileToolName(value string) bool {
	return value == "list_files" || value == "get_file_metadata" || value == "read_file_chunk"
}

func sanitizeMCPReportedClientValue(value string, maxBytes int) string {
	var cleaned strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		if cleaned.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		cleaned.WriteRune(r)
	}
	return strings.TrimSpace(cleaned.String())
}
