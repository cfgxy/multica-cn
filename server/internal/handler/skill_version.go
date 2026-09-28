package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type SkillVersionFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SkillVersionResponse struct {
	ID               string             `json:"id"`
	SkillID          string             `json:"skill_id"`
	Version          int32              `json:"version"`
	Name             string             `json:"name"`
	Description      string             `json:"description"`
	Content          string             `json:"content,omitempty"`
	Config           json.RawMessage    `json:"config,omitempty"`
	Files            []SkillVersionFile `json:"files,omitempty"`
	Source           string             `json:"source"`
	SourceVersion    *int32             `json:"source_version,omitempty"`
	SourceProposalID *string            `json:"source_proposal_id,omitempty"`
	AuthorUserID     *string            `json:"author_user_id,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
}

func lockSkillVersionTarget(ctx context.Context, tx pgx.Tx, skill db.Skill) error {
	var id pgtype.UUID
	return tx.QueryRow(ctx, `SELECT id FROM skill WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		skill.ID, skill.WorkspaceID).Scan(&id)
}

// appendSkillVersion runs inside the mutation transaction while the skill row
// is locked. The lock serializes both the content snapshot and version number.
func appendSkillVersion(ctx context.Context, tx pgx.Tx, skillID, authorID pgtype.UUID, source string, sourceVersion *int32) error {
	var workspaceID pgtype.UUID
	var name, description, content string
	var config []byte
	err := tx.QueryRow(ctx, `SELECT workspace_id, name, description, content, config
		FROM skill WHERE id = $1 FOR UPDATE`, skillID).Scan(&workspaceID, &name, &description, &content, &config)
	if err != nil {
		return err
	}
	var files []byte
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('path', path, 'content', content)
		ORDER BY path), '[]'::jsonb) FROM skill_file WHERE skill_id = $1`, skillID).Scan(&files)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO skill_version
		(workspace_id, skill_id, version, name, description, content, config, files,
		 source, source_version, author_user_id)
		SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4, $5, $6, $7, $8, $9, $10
		FROM skill_version WHERE skill_id = $2`, workspaceID, skillID, name, description,
		content, config, files, source, sourceVersion, authorID)
	return err
}

func (h *Handler) ListSkillVersions(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT id, version, name, description, source,
		source_version, source_proposal_id, author_user_id, created_at
		FROM skill_version WHERE skill_id = $1 AND workspace_id = $2 ORDER BY version DESC`,
		skill.ID, skill.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill versions")
		return
	}
	defer rows.Close()
	versions := make([]SkillVersionResponse, 0)
	for rows.Next() {
		v := SkillVersionResponse{SkillID: uuidToString(skill.ID)}
		var id, proposalID, authorID pgtype.UUID
		var sourceVersion pgtype.Int4
		if err := rows.Scan(&id, &v.Version, &v.Name, &v.Description, &v.Source,
			&sourceVersion, &proposalID, &authorID, &v.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skill versions")
			return
		}
		v.ID = uuidToString(id)
		if sourceVersion.Valid {
			v.SourceVersion = &sourceVersion.Int32
		}
		if proposalID.Valid {
			s := uuidToString(proposalID)
			v.SourceProposalID = &s
		}
		if authorID.Valid {
			s := uuidToString(authorID)
			v.AuthorUserID = &s
		}
		versions = append(versions, v)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to read skill versions")
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func skillVersionNumber(w http.ResponseWriter, r *http.Request) (int32, bool) {
	n, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 32)
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "invalid skill version")
		return 0, false
	}
	return int32(n), true
}

func (h *Handler) loadSkillVersion(ctx context.Context, skill db.Skill, version int32) (SkillVersionResponse, error) {
	v := SkillVersionResponse{SkillID: uuidToString(skill.ID)}
	var id, proposalID, authorID pgtype.UUID
	var sourceVersion pgtype.Int4
	var files []byte
	err := h.DB.QueryRow(ctx, `SELECT id, version, name, description, content, config,
		files, source, source_version, source_proposal_id, author_user_id, created_at
		FROM skill_version WHERE skill_id = $1 AND workspace_id = $2 AND version = $3`,
		skill.ID, skill.WorkspaceID, version).Scan(&id, &v.Version, &v.Name, &v.Description,
		&v.Content, &v.Config, &files, &v.Source, &sourceVersion, &proposalID, &authorID, &v.CreatedAt)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(files, &v.Files); err != nil {
		return v, err
	}
	v.ID = uuidToString(id)
	if sourceVersion.Valid {
		v.SourceVersion = &sourceVersion.Int32
	}
	if proposalID.Valid {
		s := uuidToString(proposalID)
		v.SourceProposalID = &s
	}
	if authorID.Valid {
		s := uuidToString(authorID)
		v.AuthorUserID = &s
	}
	return v, nil
}

func (h *Handler) GetSkillVersion(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	version, ok := skillVersionNumber(w, r)
	if !ok {
		return
	}
	v, err := h.loadSkillVersion(r.Context(), skill, version)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "skill version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill version")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) RestoreSkillVersion(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(skill.WorkspaceID), "skill not found", "owner"); !ok {
		return
	}
	version, ok := skillVersionNumber(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore skill")
		return
	}
	defer tx.Rollback(r.Context())
	if err := lockSkillVersionTarget(r.Context(), tx, skill); err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	v, err := h.loadSkillVersion(r.Context(), skill, version)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "skill version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill version")
		return
	}
	qtx := h.Queries.WithTx(tx)
	updatedSkill, err := qtx.UpdateSkill(r.Context(), db.UpdateSkillParams{
		ID:          skill.ID,
		Name:        pgtype.Text{String: v.Name, Valid: true},
		Description: pgtype.Text{String: v.Description, Valid: true},
		Content:     pgtype.Text{String: v.Content, Valid: true},
		Config:      v.Config,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "cannot restore skill version")
		return
	}
	if err := qtx.DeleteSkillFilesBySkill(r.Context(), skill.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore skill files")
		return
	}
	files := make([]SkillFileResponse, 0, len(v.Files))
	for _, f := range v.Files {
		sf, err := qtx.UpsertSkillFile(r.Context(), db.UpsertSkillFileParams{
			SkillID: skill.ID, Path: f.Path, Content: f.Content,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to restore skill files")
			return
		}
		files = append(files, skillFileToResponse(sf))
	}
	if err := appendSkillVersion(r.Context(), tx, skill.ID, parseUUID(userID), "restore", &version); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record skill version")
		return
	}
	var restoredVersion int32
	if err := tx.QueryRow(r.Context(), `SELECT MAX(version) FROM skill_version WHERE skill_id = $1`, skill.ID).Scan(&restoredVersion); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read restored version")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore skill")
		return
	}
	wsID := uuidToString(skill.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, wsID)
	h.publish(protocol.EventSkillUpdated, wsID, actorType, actorID, map[string]any{
		"skill": SkillWithFilesResponse{SkillResponse: skillToResponse(updatedSkill), Files: files},
	})
	writeJSON(w, http.StatusOK, map[string]any{"version": restoredVersion})
}
