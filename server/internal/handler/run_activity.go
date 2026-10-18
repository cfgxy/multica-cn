package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const (
	runActivityDefaultLimit = 50
	runActivityMaxLimit     = 100
	runActivityMessageBytes = 8 << 10
	runActivityResponseMax  = 160 << 10
)

type runActivityMessage struct {
	Seq       int32          `json:"seq"`
	Type      string         `json:"type"`
	Tool      string         `json:"tool,omitempty"`
	Content   string         `json:"content,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	Output    string         `json:"output,omitempty"`
	CreatedAt *string        `json:"created_at"`
	IsError   *bool          `json:"is_error,omitempty"`
	Truncated bool           `json:"truncated"`
}

type runActivityResponse struct {
	RunState struct {
		Status      string  `json:"status"`
		CreatedAt   *string `json:"created_at"`
		StartedAt   *string `json:"started_at"`
		CompletedAt *string `json:"completed_at"`
		ElapsedMS   *int64  `json:"elapsed_ms"`
		WaitReason  *string `json:"wait_reason"`
	} `json:"run_state"`
	SemanticActivity struct {
		Messages       []runActivityMessage `json:"messages"`
		NextAfterSeq   int32                `json:"next_after_seq"`
		HasMore        bool                 `json:"has_more"`
		LastActivityAt *string              `json:"last_activity_at"`
	} `json:"semantic_activity"`
	WorkerEvidence struct {
		LogAvailable      bool    `json:"log_available"`
		UnavailableReason string  `json:"unavailable_reason"`
		StdoutTail        *string `json:"stdout_tail"`
		StderrTail        *string `json:"stderr_tail"`
		NextCursor        *string `json:"next_cursor"`
		LastActivityAt    *string `json:"last_activity_at"`
		ProcessLiveness   string  `json:"process_liveness"`
	} `json:"worker_evidence"`
	ProgressAssessment struct {
		Status string   `json:"status"`
		Basis  []string `json:"basis"`
	} `json:"progress_assessment"`
}

// GetRunActivity exposes bounded semantic activity and the platform run state.
// Worker evidence is fetched only through the authenticated runtime connection.
func (h *Handler) GetRunActivity(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid authenticated actor")
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || task.IssueID != issue.ID {
		if !h.auditRunActivity(r, h.Queries, actorUUID, issue.WorkspaceID, issue.ID, pgtype.UUID{}, r.URL.Query().Get("stream"), 0, 16<<10, "", runActivityResponse{}, 0, "not_found") {
			writeError(w, http.StatusServiceUnavailable, "run activity audit unavailable")
			return
		}
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	afterSeq, hasCursor, err := parseRunActivityAfterSeq(r.URL.Query().Get("after_seq"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseRunActivityLimit(r.URL.Query().Get("semantic_limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if stream := r.URL.Query().Get("stream"); stream != "" && stream != "stdout" && stream != "stderr" && stream != "both" {
		writeError(w, http.StatusBadRequest, "stream must be stdout, stderr, or both")
		return
	}
	tailBytes := 16 << 10
	if raw := r.URL.Query().Get("tail_bytes"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 1 || n > 64<<10 {
			writeError(w, http.StatusBadRequest, "tail_bytes must be an integer in [1,65536]")
			return
		}
		tailBytes = n
	}
	workerCursor := r.URL.Query().Get("worker_cursor")
	if workerCursor != "" {
		if len(workerCursor) > 512 {
			writeError(w, http.StatusBadRequest, "worker_cursor is too long")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "run activity temporarily unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	lockKey := "run-activity:" + uuid.UUID(actorUUID.Bytes).String() + ":" + uuid.UUID(taskID.Bytes).String()
	if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		writeError(w, http.StatusServiceUnavailable, "run activity temporarily unavailable")
		return
	}
	reads, err := qtx.CountRecentRunActivityReads(r.Context(), db.CountRecentRunActivityReadsParams{
		ActorID: actorUUID,
		TaskID:  taskID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check run activity rate limit")
		return
	}
	if reads > 0 {
		if !h.auditRunActivity(r, qtx, actorUUID, issue.WorkspaceID, issue.ID, task.ID, r.URL.Query().Get("stream"), afterSeq, tailBytes, workerCursor, runActivityResponse{}, 0, "rate_limited") {
			writeError(w, http.StatusServiceUnavailable, "run activity audit unavailable")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "run activity audit unavailable")
			return
		}
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, "run activity rate limit exceeded")
		return
	}

	page, err := qtx.ListTaskMessagesPage(r.Context(), db.ListTaskMessagesPageParams{
		TaskID: taskID,
		Seq:    afterSeq,
		Limit:  int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read run activity")
		return
	}
	latestAt, latestErr := qtx.GetLatestTaskMessage(r.Context(), taskID)
	if latestErr != nil && !errors.Is(latestErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read run activity")
		return
	}
	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}
	resp := buildRunActivityResponse(task, page, latestAt, latestErr == nil, afterSeq, hasCursor, hasMore)
	if h.DaemonHub != nil && task.RuntimeID.Valid {
		stream := normalizedActivityStream(r.URL.Query().Get("stream"))
		proxyCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		evidence, proxyErr := h.DaemonHub.RequestWorkerEvidence(proxyCtx, uuid.UUID(task.RuntimeID.Bytes).String(), protocol.WorkerEvidenceRequest{
			RunID: uuid.UUID(task.ID.Bytes).String(), RuntimeID: uuid.UUID(task.RuntimeID.Bytes).String(), Stream: stream,
			Cursor: workerCursor, TailBytes: tailBytes,
		})
		cancel()
		if proxyErr == nil {
			if len(evidence.StdoutTail) > tailBytes || len(evidence.StderrTail) > tailBytes || len(evidence.NextCursor) > 512 {
				proxyErr = fmt.Errorf("invalid evidence response")
			}
		}
		if proxyErr == nil {
			resp.WorkerEvidence.LogAvailable = evidence.Available
			if evidence.Available {
				resp.WorkerEvidence.UnavailableReason = ""
				resp.ProgressAssessment.Basis = []string{"run_state", "worker_evidence_available"}
				stdout, stderr := redact.Text(evidence.StdoutTail), redact.Text(evidence.StderrTail)
				resp.WorkerEvidence.StdoutTail = &stdout
				resp.WorkerEvidence.StderrTail = &stderr
				resp.WorkerEvidence.NextCursor = &evidence.NextCursor
				resp.WorkerEvidence.LastActivityAt = timestampToPtr(pgtype.Timestamptz{Time: valueOrZeroTime(evidence.ObservedAt), Valid: evidence.ObservedAt != nil})
				if evidence.ProcessLiveness == "alive" || evidence.ProcessLiveness == "exited" {
					resp.WorkerEvidence.ProcessLiveness = evidence.ProcessLiveness
				} else {
					resp.WorkerEvidence.ProcessLiveness = "unknown"
				}
				applyWorkerEvidenceProgress(&resp, workerCursor != "", evidence.StdoutTail, evidence.StderrTail)
			} else {
				resp.WorkerEvidence.UnavailableReason = stableWorkerUnavailable(evidence.Unavailable)
				resp.WorkerEvidence.ProcessLiveness = "unknown"
			}
		}
	}
	var encoded []byte
	for {
		var marshalErr error
		encoded, marshalErr = json.Marshal(resp)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode run activity")
			return
		}
		if len(encoded) <= runActivityResponseMax {
			break
		}
		if len(resp.SemanticActivity.Messages) == 0 {
			writeError(w, http.StatusInternalServerError, "run activity response exceeds limit")
			return
		}
		resp.SemanticActivity.Messages = resp.SemanticActivity.Messages[:len(resp.SemanticActivity.Messages)-1]
		if len(resp.SemanticActivity.Messages) == 0 {
			resp.SemanticActivity.NextAfterSeq = afterSeq
		} else {
			resp.SemanticActivity.NextAfterSeq = resp.SemanticActivity.Messages[len(resp.SemanticActivity.Messages)-1].Seq
		}
		resp.SemanticActivity.HasMore = true
	}
	if !h.auditRunActivity(r, qtx, actorUUID, issue.WorkspaceID, issue.ID, task.ID, r.URL.Query().Get("stream"), afterSeq, tailBytes, workerCursor, resp, len(encoded), "success") {
		writeError(w, http.StatusServiceUnavailable, "run activity audit unavailable")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "run activity audit unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func applyWorkerEvidenceProgress(resp *runActivityResponse, hadCursor bool, stdout, stderr string) {
	if resp == nil || !hadCursor || (stdout == "" && stderr == "") || resp.ProgressAssessment.Status == "finished" || resp.ProgressAssessment.Status == "waiting" {
		return
	}
	resp.ProgressAssessment.Status = "advancing"
	for _, basis := range resp.ProgressAssessment.Basis {
		if basis == "worker_evidence_delta" {
			return
		}
	}
	resp.ProgressAssessment.Basis = append(resp.ProgressAssessment.Basis, "worker_evidence_delta")
}

func stableWorkerUnavailable(value string) string {
	switch value {
	case "not_supervised", "expired", "not_found", "daemon_unreachable":
		return value
	default:
		return "daemon_unreachable"
	}
}

func valueOrZeroTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func parseRunActivityAfterSeq(raw string) (int32, bool, error) {
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("after_seq must be a non-negative 32-bit integer")
	}
	return int32(n), true, nil
}

func parseRunActivityLimit(raw string) (int, error) {
	if raw == "" {
		return runActivityDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > runActivityMaxLimit {
		return 0, fmt.Errorf("semantic_limit must be an integer in [1,%d]", runActivityMaxLimit)
	}
	return n, nil
}

func buildRunActivityResponse(task db.AgentTaskQueue, rows []db.ListTaskMessagesPageRow, latestAt pgtype.Timestamptz, hasLatest bool, afterSeq int32, hasCursor, hasMore bool) runActivityResponse {
	var resp runActivityResponse
	resp.RunState.Status = task.Status
	resp.RunState.CreatedAt = timestampToPtr(task.CreatedAt)
	resp.RunState.StartedAt = timestampToPtr(task.StartedAt)
	resp.RunState.CompletedAt = timestampToPtr(task.CompletedAt)
	if task.StartedAt.Valid {
		end := time.Now().UTC()
		if task.CompletedAt.Valid {
			end = task.CompletedAt.Time
		}
		elapsed := end.Sub(task.StartedAt.Time).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		resp.RunState.ElapsedMS = &elapsed
	}
	if task.WaitReason.Valid && strings.TrimSpace(task.WaitReason.String) != "" {
		waitReason := redact.Text(util.SanitizeTextForPostgres(task.WaitReason.String))
		waitReason = truncateUTF8(waitReason, 512)
		resp.RunState.WaitReason = &waitReason
	} else if task.Status == "waiting_local_directory" {
		waitReason := task.Status
		resp.RunState.WaitReason = &waitReason
	}
	resp.SemanticActivity.Messages = make([]runActivityMessage, 0, len(rows))
	resp.SemanticActivity.NextAfterSeq = afterSeq
	for _, row := range rows {
		msg := runActivityMessage{
			Seq: row.Seq, Type: redact.Text(row.Type), Tool: redact.Text(row.Tool),
			Content: redact.Text(row.Content), Output: redact.Text(row.Output),
			CreatedAt: timestampToPtr(row.CreatedAt), IsError: validBoolPtr(row.IsError),
			Truncated: row.Truncated.Valid && row.Truncated.Bool,
		}
		if row.Input != nil {
			_ = json.Unmarshal(row.Input, &msg.Input)
			msg.Input = redact.InputMap(msg.Input)
		}
		msg = boundRunActivityMessage(msg)
		resp.SemanticActivity.Messages = append(resp.SemanticActivity.Messages, msg)
		resp.SemanticActivity.NextAfterSeq = row.Seq
	}
	resp.SemanticActivity.HasMore = hasMore
	if hasLatest {
		resp.SemanticActivity.LastActivityAt = timestampToPtr(latestAt)
	}
	resp.WorkerEvidence.LogAvailable = false
	resp.WorkerEvidence.UnavailableReason = "daemon_unreachable"
	resp.WorkerEvidence.ProcessLiveness = "unknown"
	resp.ProgressAssessment.Basis = []string{"run_state", "worker_evidence_unavailable"}
	switch {
	case task.Status == "completed" || task.Status == "failed" || task.Status == "cancelled":
		resp.ProgressAssessment.Status = "finished"
	case resp.RunState.WaitReason != nil || task.Status == "waiting_local_directory":
		resp.ProgressAssessment.Status = "waiting"
	case hasCursor && len(rows) > 0 && rows[len(rows)-1].Seq > afterSeq:
		resp.ProgressAssessment.Status = "advancing"
	case len(rows) == 0 && !hasLatest:
		resp.ProgressAssessment.Status = "unknown"
	case !hasCursor && hasLatest:
		resp.ProgressAssessment.Status = "no_recent_evidence"
	case hasCursor:
		resp.ProgressAssessment.Status = "unknown"
	default:
		resp.ProgressAssessment.Status = "unknown"
	}
	if hasLatest || resp.ProgressAssessment.Status == "advancing" {
		resp.ProgressAssessment.Basis = append(resp.ProgressAssessment.Basis, "semantic_activity")
	}
	return resp
}

func boundRunActivityMessage(msg runActivityMessage) runActivityMessage {
	encoded, _ := json.Marshal(msg)
	if len(encoded) <= runActivityMessageBytes {
		return msg
	}
	msg.Truncated = true
	msg.Input = nil
	msg.Content = truncateUTF8(msg.Content, 2048)
	msg.Output = truncateUTF8(msg.Output, 2048)
	msg.Tool = truncateUTF8(msg.Tool, 256)
	for {
		encoded, _ = json.Marshal(msg)
		if len(encoded) <= runActivityMessageBytes {
			return msg
		}
		if len(msg.Output) > 0 {
			msg.Output = truncateUTF8(msg.Output, len(msg.Output)/2)
		} else if len(msg.Content) > 0 {
			msg.Content = truncateUTF8(msg.Content, len(msg.Content)/2)
		} else {
			return msg
		}
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func validBoolPtr(value pgtype.Bool) *bool {
	if !value.Valid {
		return nil
	}
	out := value.Bool
	return &out
}

func writeRunActivityAudit(r *http.Request, q *db.Queries, actorID, workspaceID, issueID, taskID pgtype.UUID, stream string, afterSeq int32, tailBytes int, workerCursor string, response runActivityResponse, responseBytes int, result string) bool {
	details := map[string]any{
		"tool_name":         "get_run_activity",
		"stream":            normalizedActivityStream(stream),
		"after_seq":         afterSeq,
		"tail_bytes":        tailBytes,
		"semantic_messages": len(response.SemanticActivity.Messages),
		"result":            result,
		"worker_result":     response.WorkerEvidence.UnavailableReason,
	}
	if workerCursor != "" {
		cursorHash := sha256.Sum256([]byte(workerCursor))
		details["worker_cursor_sha256"] = hex.EncodeToString(cursorHash[:])
	}
	if len(response.SemanticActivity.Messages) > 0 {
		details["seq_range"] = []int32{
			response.SemanticActivity.Messages[0].Seq,
			response.SemanticActivity.Messages[len(response.SemanticActivity.Messages)-1].Seq,
		}
	}
	details["response_bytes"] = responseBytes
	event := service.Event{
		ID:          pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
		WorkspaceID: workspaceID, Domain: service.AuditDomainRun, EventType: "run.activity_read",
		OccurredAt: time.Now().UTC(), ActorType: service.AuditActorMember, ActorID: actorID,
		IssueID: issueID, TaskID: taskID, Details: service.JSONDetails(details), Sensitive: true,
	}
	return service.AppendAuditEvents(r.Context(), q, event) == nil
}

func (h *Handler) auditRunActivity(r *http.Request, q *db.Queries, actorID, workspaceID, issueID, taskID pgtype.UUID, stream string, afterSeq int32, tailBytes int, workerCursor string, response runActivityResponse, responseBytes int, result string) bool {
	if h.runActivityAuditWriter != nil {
		return h.runActivityAuditWriter(r, q, actorID, workspaceID, issueID, taskID, stream, afterSeq, tailBytes, workerCursor, response, responseBytes, result)
	}
	return writeRunActivityAudit(r, q, actorID, workspaceID, issueID, taskID, stream, afterSeq, tailBytes, workerCursor, response, responseBytes, result)
}

func normalizedActivityStream(stream string) string {
	if stream == "" {
		return "both"
	}
	return stream
}
