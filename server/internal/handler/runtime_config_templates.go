package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/sub2api"
)

func (h *Handler) runtimeTemplateFor(ctx context.Context, kind string) (computer.ModelTemplate, error) {
	var t computer.ModelTemplate
	var files, protocols []byte
	err := h.DB.QueryRow(ctx, `SELECT runtime_kind,revision,files,supported_api_types,default_model,status FROM runtime_config_template WHERE runtime_kind=$1`, kind).Scan(&t.RuntimeKind, &t.Revision, &files, &protocols, &t.DefaultModel, &t.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return computer.BuiltinModelTemplate(kind)
	}
	if err != nil {
		return t, err
	}
	if json.Unmarshal(files, &t.Files) != nil || json.Unmarshal(protocols, &t.SupportedAPITypes) != nil {
		return t, computer.ClassifiedError("model_template_invalid")
	}
	return t, t.Validate()
}
func (h *Handler) AdminRuntimeConfigTemplates(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	if !sub2api.KnownRuntime(kind) {
		writeError(w, 400, "Unknown runtime")
		return
	}
	if r.Method == http.MethodGet {
		t, err := h.runtimeTemplateFor(r.Context(), kind)
		if err != nil {
			writeError(w, 500, "Cannot read template")
			return
		}
		writeJSON(w, 200, t)
		return
	}
	var t computer.ModelTemplate
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&t) != nil {
		writeError(w, 400, "Invalid template")
		return
	}
	t.RuntimeKind = kind
	if err := t.Validate(); err != nil {
		writeError(w, 422, "Invalid template paths, formats, bindings or protocols")
		return
	}
	files, _ := json.Marshal(t.Files)
	protocols, _ := json.Marshal(t.SupportedAPITypes)
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Cannot save template")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('runtime-template-'||$1))`, kind); err != nil {
		writeError(w, 500, "Cannot lock template")
		return
	}
	var revision int64
	err = tx.QueryRow(r.Context(), `SELECT revision FROM runtime_config_template WHERE runtime_kind=$1 FOR UPDATE`, kind).Scan(&revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "Cannot read template")
		return
	}
	if revision != t.Revision {
		writeError(w, 409, "Template changed; reload before saving")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO runtime_config_template(runtime_kind,files,supported_api_types,default_model,status,updated_by) VALUES($1,$2,$3,$4,'published',$5) ON CONFLICT(runtime_kind) DO UPDATE SET revision=runtime_config_template.revision+1,files=EXCLUDED.files,supported_api_types=EXCLUDED.supported_api_types,default_model=EXCLUDED.default_model,status='published',updated_by=EXCLUDED.updated_by,updated_at=now()`, kind, files, protocols, t.DefaultModel, parseUUID(uid))
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO computer_audit(user_id,action,outcome) VALUES($1,$2,'success')`, parseUUID(uid), "model_template_save:"+kind)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "Cannot save template")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (h *Handler) AdminRuntimeConfigTemplateList(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	out := []computer.ModelTemplate{}
	for _, kind := range []string{"claude", "codex", "opencode", "openclaw", "grok", "kimi", "omp", "pi"} {
		t, err := h.runtimeTemplateFor(r.Context(), kind)
		if err != nil {
			writeError(w, 500, "Cannot read templates")
			return
		}
		out = append(out, t)
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

// Existing configurations retain their original file paths and field mapping.
// The original unrendered skeleton lets a missing file be recreated without
// applying a newer template. User contents and resolved tokens never enter it.
func (h *Handler) configTemplateFor(ctx context.Context, binding, kind string) (computer.ModelTemplate, error) {
	var manifest computer.ModelTemplate
	var raw []byte
	var revision *int64
	err := h.DB.QueryRow(ctx, `SELECT file_manifest,template_revision FROM computer_runtime_model_config WHERE binding_id=$1 AND runtime_kind=$2`, binding, kind).Scan(&raw, &revision)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && string(raw) == "[]") {
		return h.runtimeTemplateFor(ctx, kind)
	}
	if err != nil {
		return manifest, err
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.RuntimeKind != kind || len(manifest.Files) == 0 {
		return manifest, computer.ClassifiedError("model_template_invalid")
	}
	if revision != nil {
		manifest.Revision = *revision
	}
	if err := manifest.Validate(); err != nil {
		return manifest, err
	}
	manifest.Status = "existing"
	return manifest, nil
}

func configManifest(t computer.ModelTemplate) []byte {
	raw, _ := json.Marshal(t)
	return raw
}

func modelFilePaths(t computer.ModelTemplate) []string {
	out := []string{}
	for _, f := range t.Files {
		out = append(out, f.Path)
	}
	return out
}
