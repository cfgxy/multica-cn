package handler

// runtime_manual.go — manual instance registration (RUYI-425 §4.3, stage 3).
// API-backed voice instances (gemini_live) have no local binary for a daemon
// to probe, so their birth path is an authenticated POST from the desktop
// creation form instead of a daemon heartbeat. CLI families are deliberately
// rejected here: their instances are daemon-born, and a manually registered
// "CLI instance" would be a row nothing can ever launch.
//
// §4.3 field mapping on create: name (duplicates allowed — Owner decision 2),
// Runtime Type = a voice-family RuntimeProfile of this workspace (a profile
// derives many instances), model/advanced land in metadata exactly where the
// §4.3 settings PATCH reads them, registration_source is 'manual' (read-only
// thereafter), status is 'online' from birth (nothing flips it offline —
// setRuntimeOffline is daemon-report-driven and manual instances have no
// daemon) and visibility is 'public' (§4.3 v1 fixes instance visibility to
// the whole workspace). The API key is NOT part of this call: the creation
// flow stores it through PUT /credentials/{key} afterwards, which also runs
// the connectivity probe (§4.3 保存即探针).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// maxManualRuntimeNameLen bounds the §4.3 名称 field — twice the custom-name
// cap, since instance display names carry no machine-host suffix convention.
const maxManualRuntimeNameLen = 200

type CreateManualRuntimeRequest struct {
	// Name is the instance display name. §4.3: same-workspace duplicates are
	// allowed, so no uniqueness check — the row id is the identity.
	Name string `json:"name"`
	// ProfileID selects the Runtime Type (a voice-family profile of this
	// workspace). Read-only in the edit form afterwards (§4.3).
	ProfileID string `json:"profile_id"`
	// Model is optional; absent leaves metadata.model unset so the form
	// shows the placeholder default (gemini-3.8-live) exactly like §4.3.
	Model string `json:"model,omitempty"`
	// Advanced is an optional JSON object (region, generationConfig
	// overrides, VAD config…). Same bounds and shape as the §4.3 PATCH.
	Advanced json.RawMessage `json:"advanced,omitempty"`
}

// CreateManualRuntime handles POST /api/runtimes — the manual registration
// API for voice-protocol instances.
func (h *Handler) CreateManualRuntime(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	var req CreateManualRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate every field before any mutation (same all-or-nothing
	// discipline as UpdateAgentRuntime).
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len([]rune(req.Name)) > maxManualRuntimeNameLen {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.ProfileID), "profile_id")
	if !ok {
		return
	}
	profile, err := h.Queries.GetRuntimeProfileForWorkspace(r.Context(), db.GetRuntimeProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid profile_id: no such runtime profile in this workspace")
		return
	}
	if !agent.IsVoiceProtocolFamily(profile.ProtocolFamily) {
		writeError(w, http.StatusBadRequest, "manual registration only supports voice protocol families; CLI instances are registered by their daemon")
		return
	}

	// metadata gets exactly the keys the §4.3 settings form reads, so a
	// freshly created instance round-trips into the stage-2 card unchanged.
	metadata := map[string]json.RawMessage{}
	if model := strings.TrimSpace(req.Model); model != "" {
		if len([]rune(model)) > maxRuntimeModelLen {
			writeError(w, http.StatusBadRequest, "model is too long")
			return
		}
		encoded, err := json.Marshal(model)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode model")
			return
		}
		metadata["model"] = encoded
	}
	if advanced := bytes.TrimSpace(req.Advanced); len(advanced) > 0 {
		// A bare JSON `null` unmarshals into a nil map without error —
		// reject it explicitly so metadata never carries advanced:null.
		var asObject map[string]json.RawMessage
		if err := json.Unmarshal(advanced, &asObject); err != nil || asObject == nil {
			writeError(w, http.StatusBadRequest, "advanced must be a JSON object")
			return
		}
		if len(advanced) > maxRuntimeAdvancedLen {
			writeError(w, http.StatusBadRequest, "advanced is too large")
			return
		}
		metadata["advanced"] = json.RawMessage(advanced)
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode metadata")
		return
	}

	rt, err := h.Queries.CreateManualAgentRuntime(r.Context(), db.CreateManualAgentRuntimeParams{
		WorkspaceID: wsUUID,
		Name:        req.Name,
		Provider:    profile.ProtocolFamily,
		Metadata:    metaBytes,
		OwnerID:     parseUUID(userID),
		ProfileID:   profileUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register runtime instance")
		return
	}

	// Capabilities are derived server-side from the profile's Type-layer
	// declaration (§4.4) — the client never supplies them, mirroring the
	// profile-create rule. Same enriched response shape the list/get APIs
	// return, so the creation form can render the instance card directly.
	profiles := h.runtimeProfileIndex(r.Context(), wsUUID)
	writeJSON(w, http.StatusCreated, runtimeToResponseWithProfiles(rt, profiles))
}
