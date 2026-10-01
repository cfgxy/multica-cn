package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// pgTimeToPtr converts a non-null timestamptz column for JSON encoding; rows
// written through DEFAULT now() always carry valid values, and invalid ones
// encode as null rather than zero-time.
func pgTimeToPtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// Workspace skill catalog (RUYI-288). The catalog is the union read model
// every selection surface (Skill Tab, agent skill dialog, chat slash menu)
// reads: skills already authored in the workspace plus the metadata-only
// mirror of what runtime-local discovery last reported. Importing a discovery
// entry through the existing runtime-local import flow is what produces the
// real skill row with its version trail; the catalog never fabricates one.

const (
	SkillCatalogKindSkill     = "skill"
	SkillCatalogKindDiscovery = "discovery"

	SkillCatalogSourceWorkspace = "workspace"
	SkillCatalogSourceRuntime   = "runtime"
	SkillCatalogSourcePlugin    = "plugin"
)

// SkillCatalogEntry is one row of the workspace skill directory. Kind "skill"
// rows are cataloged workspace skills; kind "discovery" rows are runtime
// sightings that have not been imported yet. `source` classifies the origin:
// "workspace" (authored in-app or archive-imported), "runtime" (imported from
// runtime-local discovery or still just discovered), or "plugin" (contributed
// by a plugin installation). patent/pattern skills are ordinary workspace
// rows — one source among several, never a hardcoded universe.
type SkillCatalogEntry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`

	// Kind "skill".
	ID        string     `json:"id,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`

	// Kind "discovery".
	RuntimeID  string     `json:"runtime_id,omitempty"`
	Provider   string     `json:"provider,omitempty"`
	Root       string     `json:"root,omitempty"`
	PluginName string     `json:"plugin_name,omitempty"`
	Key        string     `json:"key,omitempty"`
	SourcePath string     `json:"source_path,omitempty"`
	FileCount  int        `json:"file_count,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// MatchingSkillID is set on discovery rows whose name collides with an
	// existing workspace skill: importing this entry will hit that skill
	// (structured conflict in the import flow), so surfaces can point the
	// user at it instead of promising a clean create.
	MatchingSkillID string `json:"matching_skill_id,omitempty"`
}

func skillCatalogSourceFromSkill(isPlugin bool, originType string) string {
	switch {
	case isPlugin:
		return SkillCatalogSourcePlugin
	case originType == "runtime_local":
		return SkillCatalogSourceRuntime
	default:
		return SkillCatalogSourceWorkspace
	}
}

// ListSkillCatalog serves GET /api/skills/catalog: the workspace's cataloged
// skills merged with the latest runtime discovery sightings. Scoped to
// workspace members; the SQL predicates below carry the tenant guard.
func (h *Handler) ListSkillCatalog(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(workspaceUUID)); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	skills, err := h.Queries.ListSkillCatalogByWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill catalog")
		return
	}
	discoveries, err := h.Queries.ListRuntimeSkillDiscoveries(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill catalog")
		return
	}

	entries := make([]SkillCatalogEntry, 0, len(skills)+len(discoveries))
	skillIDByName := make(map[string]string, len(skills))
	for _, s := range skills {
		createdBy := ""
		if s.CreatedBy.Valid {
			createdBy = uuidToString(s.CreatedBy)
		}
		entries = append(entries, SkillCatalogEntry{
			Kind:        SkillCatalogKindSkill,
			Name:        s.Name,
			Description: s.Description,
			Source:      skillCatalogSourceFromSkill(s.IsPlugin, s.OriginType),
			ID:          uuidToString(s.ID),
			CreatedBy:   createdBy,
			CreatedAt:   pgTimeToPtr(s.CreatedAt),
			UpdatedAt:   pgTimeToPtr(s.UpdatedAt),
		})
		skillIDByName[s.Name] = uuidToString(s.ID)
	}

	// The same on-disk skill is often seen by several runtimes (shared
	// ~/.agents/skills root). Collapse sightings to one catalog entry per
	// source path, keeping the freshest report.
	type sightingKey struct {
		sourcePath string
		name       string
	}
	freshest := make(map[sightingKey]db.RuntimeSkillDiscovery, len(discoveries))
	for _, d := range discoveries {
		k := sightingKey{sourcePath: d.SourcePath, name: d.Name}
		if prev, ok := freshest[k]; ok && !d.LastSeenAt.Time.After(prev.LastSeenAt.Time) {
			continue
		}
		freshest[k] = d
	}
	for _, d := range freshest {
		entry := SkillCatalogEntry{
			Kind:        SkillCatalogKindDiscovery,
			Name:        d.Name,
			Description: d.Description,
			Source:      SkillCatalogSourceRuntime,
			RuntimeID:   d.RuntimeID,
			Provider:    d.Provider,
			Root:        d.Root,
			PluginName:  d.Plugin,
			Key:         d.Key,
			SourcePath:  d.SourcePath,
			FileCount:   int(d.FileCount),
			LastSeenAt:  pgTimeToPtr(d.LastSeenAt),
		}
		if id, ok := skillIDByName[d.Name]; ok {
			entry.MatchingSkillID = id
		}
		entries = append(entries, entry)
	}

	writeJSON(w, http.StatusOK, entries)
}

// SyncSkillCatalog serves POST /api/skills/catalog/sync: it enqueues a local
// skill discovery request for every online runtime of the workspace. Daemons
// report on their heartbeat and the report handler mirrors results into the
// discovery index; clients refetch the catalog to observe the refresh. This
// is the same enqueue the runtime capability panel performs — no new daemon
// protocol.
func (h *Handler) SyncSkillCatalog(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(workspaceUUID)); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	runtimes, err := h.Queries.ListOnlineRuntimesByWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}

	triggered := 0
	for _, rt := range runtimes {
		rtID := uuidToString(rt.ID)
		if _, err := h.LocalSkillListStore.Create(r.Context(), rtID); err != nil {
			slog.Warn("skill catalog sync enqueue failed",
				"error", err, "runtime_id", rtID, "workspace_id", uuidToString(workspaceUUID))
			continue
		}
		h.requestDaemonPendingWork(rtID, protocol.PendingWorkKindLocalSkills)
		triggered++
	}

	writeJSON(w, http.StatusOK, map[string]any{"triggered": triggered})
}

// syncRuntimeSkillDiscoveries mirrors a completed discovery report into the
// workspace index: upsert every reported sighting, then prune the runtime's
// stale rows (keys it no longer reports). Best-effort by design — a sync
// failure must never fail the daemon's report round trip, it only costs
// catalog freshness until the next discovery.
func (h *Handler) syncRuntimeSkillDiscoveries(ctx context.Context, workspaceID string, runtimeID string, skills []RuntimeLocalSkillSummary) error {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(skills))
	for _, s := range skills {
		name := sanitizeNullBytes(s.Name)
		key := sanitizeNullBytes(s.Key)
		if name == "" || key == "" {
			continue
		}
		keys = append(keys, key)
		if err := h.Queries.UpsertRuntimeSkillDiscovery(ctx, db.UpsertRuntimeSkillDiscoveryParams{
			WorkspaceID: wsUUID,
			RuntimeID:   runtimeID,
			Provider:    s.Provider,
			Root:        s.Root,
			Plugin:      s.Plugin,
			Key:         key,
			Name:        name,
			Description: sanitizeNullBytes(s.Description),
			SourcePath:  s.SourcePath,
			FileCount:   int32(s.FileCount),
		}); err != nil {
			return err
		}
	}

	return h.Queries.DeleteRuntimeSkillDiscoveriesExcept(ctx, db.DeleteRuntimeSkillDiscoveriesExceptParams{
		WorkspaceID: wsUUID,
		RuntimeID:   runtimeID,
		Keys:        keys,
	})
}
