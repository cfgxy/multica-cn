package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// maxAgentWebhooksPerAgent caps the number of webhooks a single agent can
// carry. Every webhook is an unauthenticated trigger surface (the URL is the
// credential), so an unbounded list would let one agent accumulate an
// unmanageable set of public entry points. 20 comfortably covers the
// multi-repo GitHub use case; enforced server-side on create, with the
// frontend mirroring it as an inline hint.
const maxAgentWebhooksPerAgent = 20

// agentWebhookNameMaxLen / agentWebhookPromptMaxLen mirror the CHECK
// constraints on agent_webhook — validated here so a rejected request
// returns a field-level 400 instead of a raw 23514.
const (
	agentWebhookNameMaxLen   = 50
	agentWebhookPromptMaxLen = 4000
)

// agentWebhookPathPrefix is the public ingress path prefix. The token is the
// final path segment; everything before it carries no secret.
const agentWebhookPathPrefix = "/api/webhooks/agents/"

// agentWebhookPathForToken composes the ingress path for a webhook token.
func agentWebhookPathForToken(token string) string {
	return agentWebhookPathPrefix + token
}

// ── CRUD API (authenticated) ────────────────────────────────────────────────

// AgentWebhookResponse mirrors the autopilot trigger response's token policy:
// managers receive webhook_token / webhook_path / webhook_url; non-managers
// get them stripped to nil and only the pre-masked path, so the UI can render
// the recognizable masked URL without ever holding the credential.
type AgentWebhookResponse struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// WebhookPathMasked is always present: the ingress path with the token
	// replaced by the fixed-width mask (never derived from the token). Safe
	// for any viewer who can see the agent.
	WebhookPathMasked string `json:"webhook_path_masked"`
	// Manager-only credential fields; nil for non-managers.
	WebhookToken *string `json:"webhook_token,omitempty"`
	WebhookPath  *string `json:"webhook_path,omitempty"`
	// WebhookURL is absolute only when the server has MULTICA_PUBLIC_URL set.
	WebhookURL *string `json:"webhook_url,omitempty"`
}

func (h *Handler) agentWebhookToResponse(row db.AgentWebhook, includeToken bool) AgentWebhookResponse {
	resp := AgentWebhookResponse{
		ID:                uuidToString(row.ID),
		AgentID:           uuidToString(row.AgentID),
		Name:              row.Name,
		Prompt:            row.Prompt,
		Enabled:           row.Enabled,
		CreatedAt:         timestampToString(row.CreatedAt),
		UpdatedAt:         timestampToString(row.UpdatedAt),
		WebhookPathMasked: agentWebhookPathForToken(webhookURLMaskSegment),
	}
	if !includeToken {
		return resp
	}
	resp.WebhookToken = &row.Token
	path := agentWebhookPathForToken(row.Token)
	resp.WebhookPath = &path
	if h.cfg.PublicURL != "" {
		full := h.cfg.PublicURL + path
		resp.WebhookURL = &full
	}
	return resp
}

// webhookURLMaskSegment is the fixed-width mask substituted for the token in
// masked paths. Fixed length and unrelated to the token so the display leaks
// nothing about credential length or shape — mirrors the frontend's
// maskAgentWebhookUrl constant.
const webhookURLMaskSegment = "••••••••••••"

// canManageAgentRows is the read-only half of the management rule (the same
// predicate canManageAgent enforces before writes and canManageAgentEnv
// applies to env access): workspace owner/admin or the agent's owner. Used
// where a non-manager is a valid outcome (list) rather than an error, so the
// error-writing gate cannot be reused.
func (h *Handler) canManageAgentRows(r *http.Request, agent db.Agent) bool {
	member, err := h.getWorkspaceMember(r.Context(), requestUserID(r), uuidToString(agent.WorkspaceID))
	if err != nil {
		return false
	}
	return canManageAgentEnv(agent, member)
}

// ListAgentWebhooks: GET /api/agents/{id}/webhooks. Any viewer of the agent
// may list; credential fields are stripped unless the caller can manage.
func (h *Handler) ListAgentWebhooks(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	// Private-agent view gate, same as GetAgent: a member who cannot see the
	// agent must not be able to enumerate its webhooks either.
	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}
	rows, err := h.Queries.ListAgentWebhooksByAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list webhooks")
		return
	}
	includeToken := h.canManageAgentRows(r, agent)
	resp := make([]AgentWebhookResponse, len(rows))
	for i, row := range rows {
		resp[i] = h.agentWebhookToResponse(row, includeToken)
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveAgentWebhookManage loads the agent, verifies management rights and
// fetches the webhook scoped to the agent — the shared entry gate for every
// mutating webhook endpoint.
func (h *Handler) resolveAgentWebhookManage(w http.ResponseWriter, r *http.Request) (db.Agent, db.AgentWebhook, bool) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Agent{}, db.AgentWebhook{}, false
	}
	if !h.canManageAgent(w, r, agent) {
		return db.Agent{}, db.AgentWebhook{}, false
	}
	webhookID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "webhookId"), "webhook id")
	if !ok {
		return db.Agent{}, db.AgentWebhook{}, false
	}
	row, err := h.Queries.GetAgentWebhook(r.Context(), db.GetAgentWebhookParams{
		ID:          webhookID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return db.Agent{}, db.AgentWebhook{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load webhook")
		return db.Agent{}, db.AgentWebhook{}, false
	}
	if uuidToString(row.AgentID) != uuidToString(agent.ID) {
		writeError(w, http.StatusNotFound, "webhook not found")
		return db.Agent{}, db.AgentWebhook{}, false
	}
	return agent, row, true
}

type CreateAgentWebhookRequest struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

func (h *Handler) CreateAgentWebhook(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	var req CreateAgentWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, prompt, ok := validateAgentWebhookFields(w, req.Name, req.Prompt)
	if !ok {
		return
	}
	count, err := h.Queries.CountAgentWebhooksByAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count webhooks")
		return
	}
	if count >= maxAgentWebhooksPerAgent {
		writeError(w, http.StatusUnprocessableEntity, "webhook limit reached")
		return
	}
	token, err := generateWebhookToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate webhook token")
		return
	}
	row, err := h.Queries.CreateAgentWebhook(r.Context(), db.CreateAgentWebhookParams{
		ID:          dbid.NewV7(),
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
		Name:        name,
		Prompt:      prompt,
		Token:       token,
		Enabled:     true,
		CreatedBy:   parseUUID(requestUserID(r)),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook")
		return
	}
	writeJSON(w, http.StatusCreated, h.agentWebhookToResponse(row, true))
}

type UpdateAgentWebhookRequest struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// UpdateAgentWebhook: PUT /api/agents/{id}/webhooks/{webhookId}. Only name
// and prompt are editable — the URL and its token are deliberately immutable
// outside an explicit rotate, so configured external integrations never break
// behind the user's back.
func (h *Handler) UpdateAgentWebhook(w http.ResponseWriter, r *http.Request) {
	_, row, ok := h.resolveAgentWebhookManage(w, r)
	if !ok {
		return
	}
	var req UpdateAgentWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, prompt, ok := validateAgentWebhookFields(w, req.Name, req.Prompt)
	if !ok {
		return
	}
	updated, err := h.Queries.UpdateAgentWebhook(r.Context(), db.UpdateAgentWebhookParams{
		ID:     row.ID,
		Name:   name,
		Prompt: prompt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update webhook")
		return
	}
	writeJSON(w, http.StatusOK, h.agentWebhookToResponse(updated, true))
}

type SetAgentWebhookEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

func (h *Handler) SetAgentWebhookEnabled(w http.ResponseWriter, r *http.Request) {
	_, row, ok := h.resolveAgentWebhookManage(w, r)
	if !ok {
		return
	}
	var req SetAgentWebhookEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	updated, err := h.Queries.SetAgentWebhookEnabled(r.Context(), db.SetAgentWebhookEnabledParams{
		ID:      row.ID,
		Enabled: *req.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update webhook")
		return
	}
	writeJSON(w, http.StatusOK, h.agentWebhookToResponse(updated, true))
}

// RotateAgentWebhook: POST /api/agents/{id}/webhooks/{webhookId}/rotate.
// Mints a fresh token; the old URL stops working immediately.
func (h *Handler) RotateAgentWebhook(w http.ResponseWriter, r *http.Request) {
	_, row, ok := h.resolveAgentWebhookManage(w, r)
	if !ok {
		return
	}
	token, err := generateWebhookToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate webhook token")
		return
	}
	updated, err := h.Queries.RotateAgentWebhookToken(r.Context(), db.RotateAgentWebhookTokenParams{
		ID:    row.ID,
		Token: token,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rotate webhook token")
		return
	}
	writeJSON(w, http.StatusOK, h.agentWebhookToResponse(updated, true))
}

func (h *Handler) DeleteAgentWebhook(w http.ResponseWriter, r *http.Request) {
	_, row, ok := h.resolveAgentWebhookManage(w, r)
	if !ok {
		return
	}
	if err := h.Queries.DeleteAgentWebhook(r.Context(), row.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateAgentWebhookFields trims and bounds-checks the editable fields,
// writing the field-specific error and returning false when invalid.
func validateAgentWebhookFields(w http.ResponseWriter, rawName, rawPrompt string) (name, prompt string, ok bool) {
	name = strings.TrimSpace(rawName)
	prompt = strings.TrimSpace(rawPrompt)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", "", false
	}
	if len([]rune(name)) > agentWebhookNameMaxLen {
		writeError(w, http.StatusBadRequest, "name too long")
		return "", "", false
	}
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return "", "", false
	}
	if len([]rune(prompt)) > agentWebhookPromptMaxLen {
		writeError(w, http.StatusBadRequest, "prompt too long")
		return "", "", false
	}
	return name, prompt, true
}

// ── Public ingress ──────────────────────────────────────────────────────────

// HandleAgentWebhook is the public entry point for agent webhooks. It runs
// OUTSIDE the authenticated route group: the bearer token in the URL path IS
// the credential. Registered for both GET and POST.
//
// Semantics (RUYI-52, Owner ruling): the request body is NEVER read — a GET
// from a browser, a POST with an empty body, and a POST with an arbitrary
// body all behave identically. The bound prompt alone becomes the first user
// message of a brand-new chat session. There is no payload normalization, no
// dedupe (every visit must create a new session), and no signature scheme.
//
// Responses:
//   - 200 {"status":"accepted", "session_id", "task_id", "queued"}
//   - 200 {"status":"ignored",  "reason_code"}   — disabled webhook, archived
//     agent, or no usable runtime (all terminal no-ops; 200 keeps external
//     retry machinery from hammering a state the caller cannot fix)
//   - 404 {"error":"webhook not found"}          — unknown/rotated token
//     (identical message whichever it was; no existence leak)
//   - 429 {"error":"rate limit exceeded"}        — IP safety/debt gates
//   - 500 {"error":"internal error"}
//
// Logging discipline: only the webhook entity id and a result summary are
// logged. The full URL or token never enters logs, errors, or responses.
func (h *Handler) HandleAgentWebhook(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}

	// Same two-gate shape as the autopilot ingress: a high absolute ceiling
	// for all traffic, plus a bad-credential debt limiter charged only on
	// unknown tokens so a scanning client exhausts its own budget first.
	ip := h.clientIPForRateLimit(r)
	if ip != "" && h.WebhookAbsoluteIPRateLimiter != nil && !h.WebhookAbsoluteIPRateLimiter.Allow(r.Context(), ip) {
		writeWebhookRateLimit(w, r, h.WebhookAbsoluteIPRateLimiter, ip, "absolute_ip", h.Metrics)
		return
	}

	row, err := h.Queries.GetAgentWebhookByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if ip != "" && h.WebhookIPRateLimiter != nil {
				h.WebhookIPRateLimiter.Allow(r.Context(), ip)
			}
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		slog.Error("agent webhook: token lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	middleware.SetWebhookTriggerID(r, uuidToString(row.ID))

	if !row.Enabled {
		h.writeAgentWebhookIgnored(w, row.ID, "webhook_disabled")
		return
	}

	agent, err := h.Queries.GetAgent(r.Context(), row.AgentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Stale webhook (agent force-deleted outside the FK, or a
			// workspace mismatch below): resolve as not-found.
			slog.Warn("agent webhook: agent missing for webhook",
				"webhook_id", uuidToString(row.ID),
			)
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		slog.Error("agent webhook: agent lookup failed",
			"webhook_id", uuidToString(row.ID),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if uuidToString(agent.WorkspaceID) != uuidToString(row.WorkspaceID) {
		slog.Warn("agent webhook: workspace mismatch",
			"webhook_id", uuidToString(row.ID),
		)
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}

	// Terminal agent-side no-ops mirror the chat send path's verdicts. An
	// archived agent or an unusable runtime means no session can be started;
	// reporting 200 ignored (with a reason code, no detail) avoids provider
	// retry storms against a state only the workspace can repair.
	if agent.ArchivedAt.Valid {
		h.writeAgentWebhookIgnored(w, row.ID, "agent_archived")
		return
	}
	if verdict, err := service.AgentReadiness(r.Context(), h.Queries, agent); err == nil && verdict.Blocked() {
		h.writeAgentWebhookIgnored(w, row.ID, "agent_unavailable")
		return
	}

	session, sent, err := h.TaskService.StartAgentWebhookSession(r.Context(), agent, row.CreatedBy, row.Prompt)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrChatSessionArchived),
			errors.Is(err, service.ErrChatTaskAgentArchived),
			errors.Is(err, service.ErrChatTaskAgentNoRuntime):
			// Lost a race between the readiness check and the send's
			// transactional re-check — a terminal no-op for the caller.
			h.writeAgentWebhookIgnored(w, row.ID, "agent_unavailable")
		default:
			slog.Error("agent webhook: session start failed",
				"webhook_id", uuidToString(row.ID),
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	slog.Info("agent webhook: triggered",
		"webhook_id", uuidToString(row.ID),
		"session_id", uuidToString(session.ID),
		"queued", sent.Queued,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "accepted",
		"session_id": uuidToString(session.ID),
		"task_id":    uuidToString(sent.Task.ID),
		"queued":     sent.Queued,
	})
}

// writeAgentWebhookIgnored emits the uniform ignored response. The reason
// code is a fixed vocabulary (never an error string) so nothing internal can
// leak through it.
func (h *Handler) writeAgentWebhookIgnored(w http.ResponseWriter, webhookID pgtype.UUID, reasonCode string) {
	slog.Info("agent webhook: ignored",
		"webhook_id", uuidToString(webhookID),
		"reason", reasonCode,
	)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "ignored",
		"reason_code": reasonCode,
	})
}
