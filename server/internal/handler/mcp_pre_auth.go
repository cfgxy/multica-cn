package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type mcpPreAuthRejectionRequest struct {
	InvocationID string `json:"invocation_id"`
	ReasonCode   string `json:"reason_code"`
	FileToolName string `json:"file_tool_name,omitempty"`
}

func (h *Handler) RecordMCPPreAuthRejection(w http.ResponseWriter, r *http.Request) {
	if !h.requireMCPAuditService(w, r) {
		return
	}
	instanceID, ok := h.requireMCPFileAccessInstance(w, r)
	if !ok {
		return
	}
	var input mcpPreAuthRejectionRequest
	if !decodeMCPAuditRequest(w, r, &input) {
		return
	}
	invocationID, err := uuid.Parse(input.InvocationID)
	if err != nil || strings.TrimSpace(input.InvocationID) != input.InvocationID {
		writeError(w, http.StatusBadRequest, "invalid invocation_id")
		return
	}
	if input.ReasonCode != "unknown_tool" && input.ReasonCode != "missing_workspace" && input.ReasonCode != "invalid_workspace" &&
		input.ReasonCode != "unauthenticated_actor" && input.ReasonCode != "entry_rejected" {
		writeError(w, http.StatusBadRequest, "invalid reason_code")
		return
	}
	if input.FileToolName != "" && input.FileToolName != "list_files" && input.FileToolName != "get_file_metadata" && input.FileToolName != "read_file_chunk" {
		writeError(w, http.StatusBadRequest, "invalid file_tool_name")
		return
	}
	if input.ReasonCode == "unknown_tool" && input.FileToolName != "" {
		writeError(w, http.StatusBadRequest, "unknown tools must not be recorded by name")
		return
	}
	if _, err := h.Queries.DeleteExpiredMCPPreAuthRejections(r.Context(), pgtype.Timestamptz{Time: time.Now(), Valid: true}); err != nil {
		logMCPAuditWriteFailure("pre_auth_cleanup", err)
		writeError(w, http.StatusServiceUnavailable, "MCP audit storage unavailable")
		return
	}
	var toolName pgtype.Text
	if input.FileToolName != "" {
		toolName = pgtype.Text{String: input.FileToolName, Valid: true}
	}
	rows, err := h.Queries.InsertMCPPreAuthRejection(r.Context(), db.InsertMCPPreAuthRejectionParams{
		InvocationID: pgtype.UUID{Bytes: invocationID, Valid: true}, InstanceID: instanceID,
		ReasonCode: input.ReasonCode, FileToolName: toolName,
	})
	if err != nil {
		logMCPAuditWriteFailure("pre_auth", err)
		writeError(w, http.StatusServiceUnavailable, "MCP audit storage unavailable")
		return
	}
	if rows == 0 {
		writeJSON(w, http.StatusConflict, mcpAuditWriteResponse{Status: "already_recorded"})
		return
	}
	writeJSON(w, http.StatusCreated, mcpAuditWriteResponse{Status: "recorded"})
}

func (h *Handler) ListMCPPreAuthRejections(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" && h.cfg.MCPFileAccessInstanceCredentials[instanceID] == "" {
		writeError(w, http.StatusBadRequest, "invalid instance_id")
		return
	}
	reasonCode := strings.TrimSpace(r.URL.Query().Get("reason_code"))
	if reasonCode != "" && reasonCode != "unknown_tool" && reasonCode != "missing_workspace" && reasonCode != "invalid_workspace" && reasonCode != "unauthenticated_actor" && reasonCode != "entry_rejected" {
		writeError(w, http.StatusBadRequest, "invalid reason_code")
		return
	}
	since, ok := parseMCPPreAuthTime(w, r.URL.Query().Get("since"))
	if !ok {
		return
	}
	until, ok := parseMCPPreAuthTime(w, r.URL.Query().Get("until"))
	if !ok {
		return
	}
	if since.Valid && until.Valid && since.Time.After(until.Time) {
		writeError(w, http.StatusBadRequest, "since must not be after until")
		return
	}
	limit := int32(100)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = int32(parsed)
	}
	if _, err := h.Queries.DeleteExpiredMCPPreAuthRejections(r.Context(), pgtype.Timestamptz{Time: time.Now(), Valid: true}); err != nil {
		logMCPAuditWriteFailure("pre_auth_cleanup", err)
		writeError(w, http.StatusServiceUnavailable, "MCP audit storage unavailable")
		return
	}
	rows, err := h.Queries.ListMCPPreAuthRejections(r.Context(), db.ListMCPPreAuthRejectionsParams{
		InstanceID: nullableText(instanceID), ReasonCode: nullableText(reasonCode), Since: since, Until: until, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list MCP pre-auth rejections")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "limit": limit})
}

func parseMCPPreAuthTime(w http.ResponseWriter, raw string) (pgtype.Timestamptz, bool) {
	if strings.TrimSpace(raw) == "" {
		return pgtype.Timestamptz{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "time filters must use RFC3339")
		return pgtype.Timestamptz{}, false
	}
	return pgtype.Timestamptz{Time: parsed, Valid: true}, true
}

func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}
