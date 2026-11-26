package handler

import (
	"encoding/json"
	"net/http"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/sub2api"
)

// Polling never returns private configuration contents or executes a CLI.
func (h *Handler) runtimeModelOperationResult(w http.ResponseWriter, r *http.Request, uid, binding, kind, operation string) {
	id, ok := parseUUIDOrBadRequest(w, operation, "operation_id")
	if !ok {
		return
	}
	var action, state, code string
	err := h.DB.QueryRow(r.Context(), `SELECT kind,state,error_code FROM computer_operation WHERE id=$1 AND binding_id=$2 AND user_id=$3 AND runtime_id=$4 AND kind IN ('model_config_save','model_config_test')`, id, binding, uid, kind).Scan(&action, &state, &code)
	if err != nil {
		writeError(w, 404, "Operation not found")
		return
	}
	out := runtimeConfigResponse{RuntimeKind: kind, OperationID: operation, OperationState: state, Endpoints: []sub2api.Endpoint{}, Models: []sub2api.Model{}}
	if state == "queued" || state == "running" {
		writeJSON(w, 200, out)
		return
	}
	if action == "model_config_test" {
		var raw []byte
		var test computer.ModelConnectionTest
		if h.DB.QueryRow(r.Context(), `SELECT last_test_result FROM computer_runtime_model_config WHERE binding_id=$1 AND runtime_kind=$2`, binding, kind).Scan(&raw) == nil && json.Unmarshal(raw, &test) == nil && test.OperationID == operation && (state == "succeeded" || (state == "failed" && code == test.ErrorCode && test.Status == "failed")) {
			out.Test = &test
			writeJSON(w, 200, out)
			return
		}
	} else if state == "succeeded" {
		var manifest []byte
		err = h.DB.QueryRow(r.Context(), `SELECT COALESCE(upstream_id,''),model_id,file_revision,file_manifest,status FROM computer_runtime_model_config WHERE binding_id=$1 AND runtime_kind=$2 AND last_save_operation_id=$3`, binding, kind, id).Scan(&out.APIKeyID, &out.ModelID, &out.Snapshot.Revision, &manifest, &out.ConfigStatus)
		if err == nil {
			var t computer.ModelTemplate
			if json.Unmarshal(manifest, &t) != nil || t.RuntimeKind != kind || t.Validate() != nil {
				modelConfigError(w, computer.ClassifiedError("model_template_invalid"), operation)
				return
			}
			out.Snapshot.Files = map[string]*string{}
			for _, f := range t.Files {
				out.Snapshot.Files[f.Path] = nil
			}
			if out.APIKeyID != "" {
				if _, e := h.grantedModelKey(r.Context(), uid, out.APIKeyID, kind); e != nil {
					out.APIKeyID = ""
					out.ConfigStatus = "authorization_invalid"
				}
			}
			out.Saved = true
			writeJSON(w, 200, out)
			return
		}
	}
	if code == "" {
		code = "model_config_conflict"
	}
	modelConfigError(w, computer.ClassifiedError(code), operation)
}
