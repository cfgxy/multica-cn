package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const errTaskTokenWorkspaceMismatch = "task token is bound to a different workspace"

// checkTaskTokenWorkspaceTarget rejects explicit workspace selectors that do
// not match the task token before Auth overwrites X-Workspace-ID with the
// authoritative binding. Every duplicated value is checked.
func checkTaskTokenWorkspaceTarget(w http.ResponseWriter, r *http.Request, queries *db.Queries, boundWorkspaceID string) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		slog.Warn("auth: task token request has malformed query string",
			"path", r.URL.Path,
			"method", r.Method,
			"bound_workspace_id", boundWorkspaceID,
			"error", err,
		)
		writeError(w, http.StatusBadRequest, "malformed query string")
		return true
	}

	reject := func(status int, msg, selector, value string) bool {
		slog.Warn("auth: task token workspace selector rejected",
			"path", r.URL.Path,
			"method", r.Method,
			"selector", selector,
			"value", value,
			"bound_workspace_id", boundWorkspaceID,
			"status", status,
		)
		writeError(w, status, msg)
		return true
	}

	idSelectors := []struct {
		name   string
		values []string
	}{
		{"header:X-Workspace-ID", r.Header.Values("X-Workspace-ID")},
		{"query:workspace_id", query["workspace_id"]},
	}
	for _, selector := range idSelectors {
		for _, value := range selector.values {
			if value == "" {
				continue
			}
			parsed, err := util.ParseUUID(value)
			if err != nil {
				return reject(http.StatusBadRequest, "invalid workspace_id", selector.name, value)
			}
			if util.UUIDToString(parsed) != boundWorkspaceID {
				return reject(http.StatusForbidden, errTaskTokenWorkspaceMismatch, selector.name, value)
			}
		}
	}

	slugSelectors := []struct {
		name   string
		values []string
	}{
		{"header:X-Workspace-Slug", r.Header.Values("X-Workspace-Slug")},
		{"query:workspace_slug", query["workspace_slug"]},
	}
	resolved := make(map[string]string)
	for _, selector := range slugSelectors {
		for _, slug := range selector.values {
			if slug == "" {
				continue
			}
			id, ok := resolved[slug]
			if !ok {
				workspace, err := queries.GetWorkspaceBySlug(r.Context(), slug)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return reject(http.StatusNotFound, "workspace not found", selector.name, slug)
					}
					slog.Error("auth: task token workspace slug lookup failed", "path", r.URL.Path, "error", err)
					writeError(w, http.StatusInternalServerError, "failed to resolve workspace")
					return true
				}
				id = util.UUIDToString(workspace.ID)
				resolved[slug] = id
			}
			if id != boundWorkspaceID {
				return reject(http.StatusForbidden, errTaskTokenWorkspaceMismatch, selector.name, slug)
			}
		}
	}
	return false
}
