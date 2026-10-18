// RUYI-645: member-facing read-only node-ops API on a runtime detail
// surface. Every endpoint here is workspace owner/admin only (Review
// ruling 1: ps/df/journalctl are machine-face reads that exceed a single
// runtime's ownership semantics, matching MeshConsole's non-owner operator
// positioning); a private runtime keeps the 404 masking that
// requireRuntimeReadAccess provides, and a member-visible but non-admin
// caller gets 403.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/opskind"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	opsListDefaultLimit = 20
	opsListMaxLimit     = 100
)

// opsCommandResponse is the wire shape of one ops command. result carries
// the node's raw output (stdout/stderr/truncated); it is null until a
// terminal receipt lands. The Web surface labels this content as raw node
// output that may contain sensitive information (Review ruling 3).
type opsCommandResponse struct {
	ID            string          `json:"id"`
	RuntimeID     string          `json:"runtime_id"`
	Kind          string          `json:"kind"`
	Args          json.RawMessage `json:"args"`
	Status        string          `json:"status"`
	TimeoutS      int32           `json:"timeout_s"`
	LeaseEpoch    int64           `json:"lease_epoch"`
	ExitCode      *int32          `json:"exit_code"`
	Result        json.RawMessage `json:"result"`
	RejectReason  *string         `json:"reject_reason"`
	SubmissionKey *string         `json:"submission_key"`
	CreatedAt     time.Time       `json:"created_at"`
	ClaimedAt     *time.Time      `json:"claimed_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Idempotent    bool            `json:"idempotent,omitempty"`
}

func opsCommandToResponse(runtimeID string, c db.DaemonOpsCommand, idempotent bool) *opsCommandResponse {
	resp := &opsCommandResponse{
		ID:         uuidToString(c.ID),
		RuntimeID:  runtimeID,
		Kind:       c.Kind,
		Args:       json.RawMessage(c.Args),
		Status:     c.Status,
		TimeoutS:   c.TimeoutS,
		LeaseEpoch: c.LeaseEpoch,
		Idempotent: idempotent,
	}
	if c.ExitCode.Valid {
		v := c.ExitCode.Int32
		resp.ExitCode = &v
	}
	if len(c.Result) > 0 {
		resp.Result = json.RawMessage(c.Result)
	} else {
		resp.Result = json.RawMessage("null")
	}
	if c.RejectReason.Valid {
		v := c.RejectReason.String
		resp.RejectReason = &v
	}
	if c.SubmissionKey.Valid {
		v := c.SubmissionKey.String
		resp.SubmissionKey = &v
	}
	resp.CreatedAt = c.CreatedAt.Time
	if c.ClaimedAt.Valid {
		v := c.ClaimedAt.Time
		resp.ClaimedAt = &v
	}
	if c.FinishedAt.Valid {
		v := c.FinishedAt.Time
		resp.FinishedAt = &v
	}
	return resp
}

// requireRuntimeOpsAccess is the ops-face gate: runtime read access first
// (parse, load, workspace membership, private-runtime 404 masking), then
// the owner/admin-only ruling on top. A member on a visible runtime → 403.
func (h *Handler) requireRuntimeOpsAccess(w http.ResponseWriter, r *http.Request, runtimeID string) (db.AgentRuntime, bool) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "runtime_ops_api", runtimeID)
	if !ok {
		return db.AgentRuntime{}, false
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "node ops commands require workspace owner or admin")
		return db.AgentRuntime{}, false
	}
	return rt, true
}

// daemonInventoryService is one row of the node's service inventory.
type daemonInventoryService struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// daemonInventoryResponse is the wire shape of GET /ops/inventory. A daemon
// that never reported renders as unavailable with error "no report
// received" — never an empty success list — so the Web surface can
// distinguish "collector broken" from "zero services" (assertion 2).
type daemonInventoryResponse struct {
	DaemonID      string                   `json:"daemon_id"`
	CollectStatus string                   `json:"collect_status"`
	Services      []daemonInventoryService `json:"services"`
	Error         *string                  `json:"error"`
	LastReportAt  *time.Time               `json:"last_report_at"`
	LastSuccessAt *time.Time               `json:"last_success_at"`
}

// GetRuntimeOpsInventory serves GET /api/runtimes/{runtimeId}/ops/inventory.
func (h *Handler) GetRuntimeOpsInventory(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}

	daemonID := textValue(rt.DaemonID)
	inv, err := h.Queries.GetDaemonServiceInventory(r.Context(), daemonID)
	if err != nil {
		writeJSON(w, http.StatusOK, daemonInventoryResponse{
			DaemonID:      daemonID,
			CollectStatus: "unavailable",
			Services:      []daemonInventoryService{},
			Error:         strToPtr("no report received"),
		})
		return
	}

	resp := daemonInventoryResponse{
		DaemonID:      inv.DaemonID,
		CollectStatus: inv.CollectStatus,
	}
	if inv.Error.Valid {
		resp.Error = &inv.Error.String
	}
	resp.LastReportAt = timePtr(inv.LastReportAt)
	resp.LastSuccessAt = timePtr(inv.LastSuccessAt)
	if err := json.Unmarshal(inv.Services, &resp.Services); err != nil || resp.Services == nil {
		resp.Services = []daemonInventoryService{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SubmitRuntimeOpsCommand serves POST /api/runtimes/{runtimeId}/ops/commands.
// Body: {kind, args, timeout_s?, submission_key?}. Fresh submissions → 201;
// a same-key-same-args replay inside the idempotency window → 200 with the
// original command and idempotent=true.
func (h *Handler) SubmitRuntimeOpsCommand(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}

	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "workspace not found")
	if !ok {
		return
	}

	var body struct {
		Kind          string          `json:"kind"`
		Args          json.RawMessage `json:"args"`
		TimeoutS      int             `json:"timeout_s"`
		SubmissionKey string          `json:"submission_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Args) > opskind.MaxArgsJSONBytes {
		writeError(w, http.StatusBadRequest, "args exceeds 4KB")
		return
	}

	submitted, err := service.SubmitOpsCommand(r.Context(), h.Queries, rt, service.OpsCommandInput{
		RuntimeID:     rt.ID,
		WorkspaceID:   rt.WorkspaceID,
		DaemonID:      textValue(rt.DaemonID),
		Kind:          body.Kind,
		ArgsJSON:      body.Args,
		TimeoutS:      body.TimeoutS,
		SubmissionKey: body.SubmissionKey,
		Submitter: service.OpsSubmitter{
			Type: service.AuditActorMember,
			ID:   member.UserID,
			Role: member.Role,
		},
	})
	if err != nil {
		h.writeOpsSubmitError(w, err)
		return
	}

	// Wake the daemon for a low-latency claim; correctness rides on the
	// heartbeat ack claim, the hint is advisory only.
	if !submitted.Idempotent {
		h.requestDaemonPendingWork(runtimeID, protocol.PendingWorkKindOpsCommand)
	}

	status := http.StatusCreated
	if submitted.Idempotent {
		status = http.StatusOK
	}
	writeJSON(w, status, opsCommandToResponse(runtimeID, submitted.Command, submitted.Idempotent))
}

// writeOpsSubmitError maps the gate pipeline's sentinel errors to the HTTP
// statuses the design fixes (matrix group 2): whitelist/slot/unit/timeout →
// 400, offline/capability/key-conflict → 409, quota → 429.
func (h *Handler) writeOpsSubmitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrOpsRuntimeOffline),
		errors.Is(err, service.ErrOpsCapabilityMissing),
		errors.Is(err, service.ErrOpsSubmissionConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrOpsKindNotAllowed),
		errors.Is(err, service.ErrOpsSlotInvalid),
		errors.Is(err, service.ErrOpsUnitNotManaged),
		errors.Is(err, service.ErrOpsTimeoutInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrOpsQuotaExceeded):
		writeError(w, http.StatusTooManyRequests, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to submit ops command")
	}
}

// ListRuntimeOpsCommands serves GET /api/runtimes/{runtimeId}/ops/commands.
// The list is also a lazy sweep point: drift states (expired/timeout)
// finalize before reading so the panel never shows a pending row that is
// already dead.
func (h *Handler) ListRuntimeOpsCommands(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}

	limit := opsListDefaultLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, opsListMaxLimit)
	}

	service.SweepOpsCommands(r.Context(), h.Queries)

	rows, err := h.Queries.ListOpsCommandsByRuntime(r.Context(), db.ListOpsCommandsByRuntimeParams{
		RuntimeID: rt.ID,
		Limit:     int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list ops commands")
		return
	}
	out := make([]*opsCommandResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, opsCommandToResponse(runtimeID, row, false))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetRuntimeOpsCommand serves GET /api/runtimes/{runtimeId}/ops/commands/{commandId}.
func (h *Handler) GetRuntimeOpsCommand(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireRuntimeOpsAccess(w, r, runtimeID)
	if !ok {
		return
	}

	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "commandId"), "command_id")
	if !ok {
		return
	}
	command, err := h.Queries.GetOpsCommand(r.Context(), id)
	if err != nil || command.RuntimeID != rt.ID {
		writeError(w, http.StatusNotFound, "command not found")
		return
	}
	writeJSON(w, http.StatusOK, opsCommandToResponse(runtimeID, command, false))
}

func textValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func strToPtr(s string) *string { return &s }
