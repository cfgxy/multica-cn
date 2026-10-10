package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/sub2api"
)

type runtimeConfigBinding struct {
	ID, MachineID, Host, SSHUser, Username, Workspace string
	Port, HealthPort                                  int
}

func (h *Handler) runtimeConfigBinding(ctx context.Context, id, uid string) (runtimeConfigBinding, error) {
	var b runtimeConfigBinding
	err := h.DB.QueryRow(ctx, `SELECT b.id::text,c.id::text,c.host,c.port,c.ssh_user,b.username,COALESCE(b.workspace_id::text,''),b.health_port FROM computer_binding b JOIN computer c ON c.id=b.computer_id WHERE b.id=$1 AND b.user_id=$2 AND b.verified AND b.archived_at IS NULL AND c.enabled`, id, uid).Scan(&b.ID, &b.MachineID, &b.Host, &b.Port, &b.SSHUser, &b.Username, &b.Workspace, &b.HealthPort)
	return b, err
}

type runtimeConfigRequest struct {
	OperationID      string            `json:"operation_id"`
	Password         string            `json:"password"`
	Files            map[string]string `json:"files"`
	APIKeyID         string            `json:"api_key_id"`
	ModelID          string            `json:"model_id"`
	APIType          string            `json:"api_type"`
	ExpectedRevision string            `json:"expected_revision"`
	CreateOnly       bool              `json:"create_only"`
}
type runtimeConfigResponse struct {
	OperationState   string                        `json:"operation_state,omitempty"`
	ConfigStatus     string                        `json:"config_status"`
	RuntimeKind      string                        `json:"runtime_kind"`
	TemplateRevision int64                         `json:"template_revision"`
	Snapshot         computer.ModelConfigSnapshot  `json:"snapshot"`
	Saved            bool                          `json:"saved"`
	Valid            bool                          `json:"valid"`
	APIKeyID         string                        `json:"api_key_id"`
	ModelID          string                        `json:"model_id"`
	APIType          string                        `json:"api_type"`
	Endpoints        []sub2api.Endpoint            `json:"endpoints"`
	Models           []sub2api.Model               `json:"models"`
	OperationID      string                        `json:"operation_id"`
	Test             *computer.ModelConnectionTest `json:"test,omitempty"`
}

func modelConfigError(w http.ResponseWriter, err error, operationIDs ...string) {
	code := computer.ErrorCode(err, "model_config_invalid")
	status := 422
	switch code {
	case "model_key_operation_failed":
		status = 500
	case "password_mismatch":
		status = 403
	case "password_locked":
		status = 429
	case "model_config_conflict", "model_config_exists", "model_key_changed":
		status = 409
	case "ssh_timeout":
		status = 504
	case "ssh_unreachable":
		status = 502
	}
	out := map[string]string{"code": code, "error": computer.ErrorSummary(code)}
	if len(operationIDs) > 0 {
		out["operation_id"] = operationIDs[0]
	}
	writeJSON(w, status, out)
}
func (h *Handler) RuntimeModelConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid, bindingID, ok := h.bindingAccess(w, r, false)
	if !ok {
		return
	}
	kind, action := chi.URLParam(r, "kind"), chi.URLParam(r, "action")
	if !sub2api.KnownRuntime(kind) || !sub2api.Contains([]string{"read", "preview", "connection", "check", "save", "test", "result"}, action) {
		writeError(w, 400, "Unknown runtime configuration action")
		return
	}
	var in runtimeConfigRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || len(in.Password) > 4096 {
		writeError(w, 400, "Invalid configuration request")
		return
	}
	if action == "result" {
		h.runtimeModelOperationResult(w, r, uid, bindingID, kind, in.OperationID)
		return
	}
	t, err := h.configTemplateFor(r.Context(), bindingID, kind)
	if err != nil {
		modelConfigError(w, err)
		return
	}
	result := runtimeConfigResponse{RuntimeKind: kind, TemplateRevision: t.Revision, Endpoints: []sub2api.Endpoint{}, Models: []sub2api.Model{}}
	if action == "check" {
		if err = computer.CheckModelFiles(t, in.Files); err != nil {
			modelConfigError(w, err)
			return
		}
		result.Valid = true
		writeJSON(w, 200, result)
		return
	}
	if in.Password == "" {
		writeError(w, 400, "Linux password is required")
		return
	}
	b, err := h.runtimeConfigBinding(r.Context(), bindingID, uid)
	if err != nil {
		writeError(w, 409, "Verify the Linux account before configuring a runtime")
		return
	}
	key, locks := os.Getenv("MULTICA_COMPUTER_SSH_KEY"), os.Getenv("MULTICA_COMPUTER_STATE_DIR")
	if key == "" || locks == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	op, err := h.beginBindingOperation(r.Context(), uid, bindingID, "model_config_"+action, kind, "")
	if err != nil {
		operationStartError(w, err)
		return
	}
	result.OperationID = op
	var actionErr error
	completed := false
	work := func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		remote := computer.SSHRemote{Host: b.Host, Port: b.Port, User: b.SSHUser, KeyPath: key, DaemonID: b.ID, WorkspaceID: b.Workspace, HealthPort: b.HealthPort, Context: ctx, Timeout: 85 * time.Second}
		actionErr = computer.WithPassword(remote, &computer.FileStore{Dir: locks}, b.MachineID, b.Username, in.Password, func() error {
			h.operationStep(op, "model_config_"+action)
			if action == "connection" {
				resolved, e := h.resolveGrantedModelKey(ctx, uid, in.APIKeyID, kind)
				if e != nil {
					return computer.ClassifiedError(modelKeyErrorCode(e))
				}
				result.APIKeyID = resolved.ID
				for _, endpoint := range resolved.Endpoints {
					if sub2api.Contains(t.SupportedAPITypes, endpoint.APIType) {
						result.Endpoints = append(result.Endpoints, endpoint)
					}
				}
				result.Models = resolved.Models
				return nil
			}
			current, e := readRuntimeModelSnapshot(remote, b.Username, &t)
			if e != nil {
				return e
			}
			result.Snapshot = current
			if action == "read" {
				var savedRevision string
				var raw []byte
				err := h.DB.QueryRow(ctx, `SELECT COALESCE(upstream_id,''),model_id,file_revision,last_test_result,status FROM computer_runtime_model_config WHERE binding_id=$1 AND runtime_kind=$2`, bindingID, kind).Scan(&result.APIKeyID, &result.ModelID, &savedRevision, &raw, &result.ConfigStatus)
				if err == nil && savedRevision == current.Revision {
					if result.APIKeyID != "" {
						if _, grantErr := h.grantedModelKey(ctx, uid, result.APIKeyID, kind); grantErr != nil {
							result.APIKeyID = ""
							result.ConfigStatus = "authorization_invalid"
						}
					}
					var test computer.ModelConnectionTest
					if json.Unmarshal(raw, &test) == nil && test.FileRevision == current.Revision && test.Status != "" {
						result.Test = &test
					}
				} else {
					result.APIKeyID = ""
					result.ModelID = ""
				}
				return nil
			}
			if current.Revision != in.ExpectedRevision {
				return computer.ClassifiedError("model_config_conflict")
			}
			if action == "preview" {
				resolved, e := h.resolveGrantedModelKey(ctx, uid, in.APIKeyID, kind)
				if e != nil {
					return computer.ClassifiedError(modelKeyErrorCode(e))
				}
				var existing map[string]string
				if current.Exists() || len(in.Files) > 0 {
					existing = in.Files
					if len(existing) == 0 {
						existing = current.Contents()
					}
				}
				generated, model, protocol, e := computer.RenderModelFiles(t, resolved, in.ModelID, in.APIType, existing)
				if e != nil {
					return e
				}
				result.Snapshot.Files = map[string]*string{}
				for p, c := range generated {
					value := c
					result.Snapshot.Files[p] = &value
				}
				result.APIKeyID = resolved.ID
				result.ModelID = model
				result.APIType = protocol
				return nil
			}
			if action == "test" {
				if !current.Exists() {
					return computer.ClassifiedError("model_config_incomplete")
				}
				if e = computer.ValidateModelFiles(t, current.Contents()); e != nil {
					return e
				}
				test, e := remote.TestModelConnection(b.Username, kind, current, computer.ConfiguredModel(t, current.Contents()), computer.ConfiguredModelSelector(t, current.Contents()))
				if e != nil {
					return e
				}
				after, readErr := readRuntimeModelSnapshot(remote, b.Username, &t)
				if readErr != nil {
					return readErr
				}
				if after.Revision != current.Revision {
					return computer.ClassifiedError("model_config_conflict")
				}
				test.OperationID = op
				raw, _ := json.Marshal(test)
				_, e = h.DB.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,last_test_status,last_test_error_code,last_tested_at,last_test_result,template_revision,file_revision,file_manifest,status) VALUES($1,$2,$3,$4,now(),$5,$6,$7,$8,'saved') ON CONFLICT(binding_id,runtime_kind) DO UPDATE SET last_test_status=$3,last_test_error_code=$4,last_tested_at=now(),last_test_result=$5,source_id=CASE WHEN computer_runtime_model_config.file_revision=$7 THEN computer_runtime_model_config.source_id ELSE NULL END,upstream_id=CASE WHEN computer_runtime_model_config.file_revision=$7 THEN computer_runtime_model_config.upstream_id ELSE NULL END,model_id=$9,last_save_operation_id=CASE WHEN computer_runtime_model_config.file_revision=$7 THEN computer_runtime_model_config.last_save_operation_id ELSE NULL END,file_revision=$7,file_manifest=$8,status=CASE WHEN computer_runtime_model_config.file_revision=$7 AND computer_runtime_model_config.status='authorization_invalid' THEN 'authorization_invalid' ELSE 'saved' END`, bindingID, kind, test.Status, test.ErrorCode, raw, t.Revision, current.Revision, configManifest(t), test.Model)
				if e != nil {
					return computer.ClassifiedError("model_config_metadata_failed")
				}
				result.Test = &test
				result.Snapshot = computer.ModelConfigSnapshot{}
				if test.Status != "succeeded" {
					return computer.ClassifiedError(test.ErrorCode)
				}
				return nil
			}
			if action != "save" {
				return computer.ClassifiedError("model_config_invalid")
			}
			if e = computer.ValidateModelFileEdits(t, in.Files); e != nil {
				return e
			}
			// Selections are only recorded when a fresh resolve and a field comparison
			// prove that the submitted files still contain the selected credential.
			var sourceID any
			var upstreamID any
			model, protocol := computer.ConfiguredModel(t, in.Files), ""
			if in.APIKeyID != "" {
				resolved, e := h.resolveGrantedModelKey(ctx, uid, in.APIKeyID, kind)
				if e != nil {
					return computer.ClassifiedError(modelKeyErrorCode(e))
				}
				expected, m, p, e := computer.RenderModelFiles(t, resolved, in.ModelID, in.APIType, in.Files)
				if e != nil {
					return e
				}
				if !sameModelFiles(expected, in.Files) {
					return computer.ClassifiedError("model_key_changed")
				}
				sourceID = modelKeySourceID
				upstreamID = resolved.ID
				model = m
				protocol = p
			}
			result.Snapshot, e = remote.SaveModelConfig(b.Username, modelFilePaths(t), in.Files, current.Revision, in.CreateOnly)
			if e != nil {
				return e
			}
			configStatus := "saved"
			if computer.ValidateModelFiles(t, result.Snapshot.Contents()) != nil {
				configStatus = "needs_configuration"
			}
			_, e = h.DB.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,source_id,upstream_id,model_id,template_revision,file_revision,file_manifest,status,last_save_operation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(binding_id,runtime_kind) DO UPDATE SET source_id=EXCLUDED.source_id,upstream_id=EXCLUDED.upstream_id,model_id=EXCLUDED.model_id,file_revision=EXCLUDED.file_revision,file_manifest=EXCLUDED.file_manifest,status=EXCLUDED.status,last_save_operation_id=EXCLUDED.last_save_operation_id,last_test_status='untested',last_test_error_code='',last_tested_at=NULL,last_test_result='{}'::jsonb,updated_at=now()`, bindingID, kind, sourceID, upstreamID, model, t.Revision, result.Snapshot.Revision, configManifest(t), configStatus, op)
			if e != nil {
				return computer.ClassifiedError("model_config_metadata_failed")
			}
			result.Saved = true
			result.ModelID = model
			result.APIType = protocol
			if upstreamID != nil {
				result.APIKeyID = in.APIKeyID
			}
			return nil
		})
		completed = true
		return "", actionErr
	}
	if (action == "save" || action == "test") && strings.Contains(r.Header.Get("Prefer"), "respond-async") {
		// The worker owns secret request values only in memory. Polling exposes
		// metadata only; no password or file body is persisted as a task payload.
		accepted := runtimeConfigResponse{RuntimeKind: kind, OperationID: op, OperationState: "queued", Endpoints: []sub2api.Endpoint{}, Models: []sub2api.Model{}}
		go h.runRemoteOperation(op, work)
		w.Header().Set("Preference-Applied", "respond-async")
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	h.runRemoteOperationContext(r.Context(), op, work)
	if !completed {
		modelConfigError(w, computer.ClassifiedError("interrupted"))
		return
	}
	if actionErr != nil {
		modelConfigError(w, actionErr, op)
		return
	}
	writeJSON(w, 200, result)
}
func sameModelFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for p, v := range a {
		if b[p] != v {
			return false
		}
	}
	return true
}

// Called only while the existing installation operation owns the account lock.
func (h *Handler) initializeRuntimeModelConfig(ctx context.Context, uid, bindingID, username, kind string, remote computer.SSHRemote) error {
	t, err := h.runtimeTemplateFor(ctx, kind)
	if err != nil {
		return err
	}
	snapshot, err := readRuntimeModelSnapshot(remote, username, &t)
	if err != nil {
		return err
	}
	if snapshot.Exists() {
		status := "saved"
		if computer.ValidateModelFiles(t, snapshot.Contents()) != nil {
			status = "needs_configuration"
		}
		_, err = h.DB.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,template_revision,file_revision,file_manifest,status) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(binding_id,runtime_kind) DO UPDATE SET template_revision=EXCLUDED.template_revision,file_revision=EXCLUDED.file_revision,file_manifest=EXCLUDED.file_manifest,status=EXCLUDED.status WHERE computer_runtime_model_config.file_revision=''`, bindingID, kind, t.Revision, snapshot.Revision, configManifest(t), status)
		return err
	}
	if err = h.refreshModelKeyDirectory(ctx, false); err != nil {
		return err
	}
	var id string
	err = h.DB.QueryRow(ctx, `SELECT g.upstream_id FROM user_model_key_grant g JOIN model_key_directory_entry d ON d.source_id=g.source_id AND d.upstream_id=g.upstream_id WHERE g.user_id=$1 AND d.status='enabled' AND d.allowed_runtimes ? $2 ORDER BY g.position,g.upstream_id LIMIT 1`, uid, kind).Scan(&id)
	if err != nil {
		return computer.ClassifiedError("model_key_unavailable")
	}
	resolved, err := h.resolveGrantedModelKey(ctx, uid, id, kind)
	if err != nil {
		return err
	}
	files, model, _, err := computer.RenderModelFiles(t, resolved, "", "", nil)
	if err != nil {
		return err
	}
	snapshot, err = remote.SaveModelConfig(username, modelFilePaths(t), files, snapshot.Revision, true)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,source_id,upstream_id,model_id,template_revision,file_revision,file_manifest,status,last_test_status,last_test_error_code,last_test_result) VALUES($1,$2,1,$3,$4,$5,$6,$7,'saved','untested','', '{}'::jsonb) ON CONFLICT(binding_id,runtime_kind) DO UPDATE SET source_id=1,upstream_id=EXCLUDED.upstream_id,model_id=EXCLUDED.model_id,template_revision=EXCLUDED.template_revision,file_revision=EXCLUDED.file_revision,file_manifest=EXCLUDED.file_manifest,status='saved',last_test_status='untested',last_test_error_code='',last_tested_at=NULL,last_test_result='{}'::jsonb`, bindingID, kind, id, model, t.Revision, snapshot.Revision, configManifest(t))
	return err
}

// OpenCode loads both formats, with JSONC taking precedence. Keep both files
// visible and preserve the base JSON file when managing the JSONC override.
func readRuntimeModelSnapshot(remote computer.SSHRemote, username string, t *computer.ModelTemplate) (computer.ModelConfigSnapshot, error) {
	if t.RuntimeKind == "opencode" && t.Status != "existing" && len(t.Files) == 1 && t.Files[0].Path == ".config/opencode/opencode.json" {
		paths := []string{t.Files[0].Path, ".config/opencode/opencode.jsonc"}
		snapshot, err := remote.ReadModelConfig(username, paths)
		if err != nil {
			return snapshot, err
		}
		if snapshot.Files[paths[1]] != nil {
			original := t.Files[0]
			t.Files[0].Path = paths[1]
			t.Files[0].Format = "jsonc"
			if snapshot.Files[paths[0]] != nil {
				original.ID = "base-json"
				original.Bindings = map[string]string{}
				t.Files = append(t.Files, original)
			}
		}
	}
	return remote.ReadModelConfig(username, modelFilePaths(*t))
}
