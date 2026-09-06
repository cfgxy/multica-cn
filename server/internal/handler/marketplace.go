package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// requireMarketplaceV1 closes both marketplace endpoints when the flag is off.
//
// It gates only discovery and install orchestration. Skill import, skill
// management, the workspace MCP library, and agent binding each keep their own
// endpoints and are untouched by this switch — the marketplace never became
// the way to reach them, so turning it off removes a front door rather than a
// capability.
func (h *Handler) requireMarketplaceV1(w http.ResponseWriter, r *http.Request) bool {
	if featureflags.MarketplaceV1Enabled(r.Context(), h.FeatureFlags) {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "The marketplace is not enabled")
	return false
}

// MarketplaceItemResponse is one catalog entry as the listing shows it, plus
// whether this workspace already has it.
//
// It deliberately carries the MCP config TEMPLATE and never a stored entry: the
// template holds `${placeholder}` tokens, not values, so listing the
// marketplace can never become a way to read back a workspace's credentials
// (the same line WorkspaceMcpServerResponse draws).
type MarketplaceItemResponse struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Publisher   string   `json:"publisher"`
	HomepageURL string   `json:"homepage_url"`
	Categories  []string `json:"categories"`
	SourceURL   string   `json:"source_url,omitempty"`

	// Placeholders tells the install dialog which values to collect and which
	// of them to mask. The template itself is not returned — the server renders
	// it — so a client cannot be tricked into displaying a fabricated command.
	Placeholders []MarketplacePlaceholderResponse `json:"placeholders,omitempty"`

	// Installed reports whether this workspace already has the skill or MCP
	// server this entry installs, and InstalledID points at it so the UI can
	// link through to the thing it created.
	Installed   bool   `json:"installed"`
	InstalledID string `json:"installed_id,omitempty"`
}

type MarketplacePlaceholderResponse struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

func marketplacePlaceholders(item service.MarketplaceItem) []MarketplacePlaceholderResponse {
	if len(item.Placeholders) == 0 {
		return nil
	}
	out := make([]MarketplacePlaceholderResponse, 0, len(item.Placeholders))
	for _, p := range item.Placeholders {
		out = append(out, MarketplacePlaceholderResponse{
			Key:         p.Key,
			Label:       p.Label,
			Description: p.Description,
			Secret:      p.Secret,
			Required:    p.Required,
		})
	}
	return out
}

// marketplaceItemMatches decides whether a search term selects an entry.
// Matching is local and case-insensitive over the catalog's own text: the
// unified marketplace must stay searchable when an upstream registry is down,
// which is exactly the failure mode SearchSkills surfaces as a 502.
func marketplaceItemMatches(item service.MarketplaceItem, query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		item.Name, item.Summary, item.Description, item.Publisher,
		strings.Join(item.Categories, " "),
	}, " "))
	return strings.Contains(haystack, strings.ToLower(query))
}

// ListMarketplaceItems returns the curated catalog, annotated with what this
// workspace already installed. Member-visible: the payload carries no
// credential material, and seeing what is available is not a write.
func (h *Handler) ListMarketplaceItems(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	items, err := service.MarketplaceCatalog()
	if err != nil {
		slog.Error("marketplace catalog failed to load", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load the marketplace catalog")
		return
	}

	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" && kind != service.MarketplaceKindSkill && kind != service.MarketplaceKindMcp {
		writeError(w, http.StatusBadRequest, `kind must be "skill" or "mcp"`)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	installedSkills, err := h.installedSkillIDsByName(r, workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load installed skills")
		return
	}
	installedMcp, err := h.installedMcpIDsByName(r, workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load installed MCP servers")
		return
	}

	resp := make([]MarketplaceItemResponse, 0, len(items))
	for _, item := range items {
		if kind != "" && item.Kind != kind {
			continue
		}
		if !marketplaceItemMatches(item, query) {
			continue
		}
		entry := MarketplaceItemResponse{
			Key:          item.Key,
			Kind:         item.Kind,
			Name:         item.Name,
			Summary:      item.Summary,
			Description:  item.Description,
			Publisher:    item.Publisher,
			HomepageURL:  item.HomepageURL,
			Categories:   item.Categories,
			SourceURL:    item.SourceURL,
			Placeholders: marketplacePlaceholders(item),
		}
		// Installed state is keyed on the name the install would take, which
		// is the same key the underlying uniqueness constraint uses. An entry
		// installed under a renamed name (import on_conflict=rename) therefore
		// reads as not installed — correct, since installing again would
		// produce another distinct skill rather than collide.
		switch item.Kind {
		case service.MarketplaceKindSkill:
			entry.InstalledID, entry.Installed = installedSkills[item.Name]
		case service.MarketplaceKindMcp:
			entry.InstalledID, entry.Installed = installedMcp[item.Name]
		}
		resp = append(resp, entry)
	}
	writeJSON(w, http.StatusOK, resp)
}

// installedSkillIDsByName indexes the workspace's skills by name — the key an
// install would collide on.
func (h *Handler) installedSkillIDsByName(r *http.Request, workspaceUUID pgtype.UUID) (map[string]string, error) {
	skills, err := h.Queries.ListSkillSummariesByWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(skills))
	for _, s := range skills {
		out[s.Name] = uuidToString(s.ID)
	}
	return out, nil
}

// installedMcpIDsByName indexes the workspace's MCP library by name. Only the
// id and name are read; the config column stays where it is.
func (h *Handler) installedMcpIDsByName(r *http.Request, workspaceUUID pgtype.UUID) (map[string]string, error) {
	servers, err := h.Queries.ListWorkspaceMcpServers(r.Context(), workspaceUUID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(servers))
	for _, s := range servers {
		out[s.Name] = uuidToString(s.ID)
	}
	return out, nil
}

// MarketplaceInstallRequest is what an install submits. `values` carries the
// placeholder values for an MCP entry — including secrets, which is why this
// request body is never logged and never echoed back.
type MarketplaceInstallRequest struct {
	Key    string            `json:"key"`
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

// InstallMarketplaceItem installs one catalog entry into the workspace.
//
// It does not create a new runtime path: a skill entry is fetched and stored
// through the same import tail every hand-typed skill import uses, and an MCP
// entry becomes an ordinary workspace MCP library row. Everything downstream —
// binding a skill to an agent, binding an MCP server to an agent, how a
// runtime discovers either — is untouched, so an installed item is
// indistinguishable from one added by hand.
func (h *Handler) InstallMarketplaceItem(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// Installing writes to the workspace's shared library, so it takes the same
	// gate adding an MCP server by hand takes: a human owner/admin. An agent
	// actor is refused even when running under an owner's token.
	if !h.requireWorkspaceMcpWriter(w, r, workspaceID) {
		return
	}

	var req MarketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	item, found := service.FindMarketplaceItem(strings.TrimSpace(req.Key))
	if !found {
		writeError(w, http.StatusNotFound, "unknown marketplace item")
		return
	}

	switch item.Kind {
	case service.MarketplaceKindSkill:
		h.installMarketplaceSkill(w, r, workspaceID, workspaceUUID, parseUUID(creatorID), creatorID, item)
	case service.MarketplaceKindMcp:
		h.installMarketplaceMcp(w, r, workspaceID, workspaceUUID, item, req)
	default:
		writeError(w, http.StatusBadRequest, "unsupported marketplace item")
	}
}

// installMarketplaceSkill fetches the catalog entry's source and hands it to
// finishSkillImport, so caps, provenance, and the same-name conflict path are
// the import endpoint's, not a second implementation.
func (h *Handler) installMarketplaceSkill(w http.ResponseWriter, r *http.Request, workspaceID string, workspaceUUID, creatorUUID pgtype.UUID, creatorID string, item service.MarketplaceItem) {
	imported, status, msg := fetchSkillFromURL(r.Context(), item.SourceURL)
	if msg != "" {
		writeError(w, status, msg)
		return
	}
	// The catalog names the entry; the upstream bundle does not get to rename
	// what the listing said it would install.
	imported.name = item.Name
	h.finishSkillImport(w, r, workspaceID, workspaceUUID, creatorUUID, creatorID, importOnConflictFail, true, imported)
}

// installMarketplaceMcp renders the catalog template with the supplied values
// and stores it as a workspace MCP library entry — unbound, exactly like one
// created by hand. Binding it to an agent stays a separate, explicit act.
func (h *Handler) installMarketplaceMcp(w http.ResponseWriter, r *http.Request, workspaceID string, workspaceUUID pgtype.UUID, item service.MarketplaceItem, req MarketplaceInstallRequest) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = item.Name
	}
	if err := validateWorkspaceMcpServerName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := service.RenderMarketplaceMcpConfig(item, req.Values)
	if err != nil {
		// The render error is written by RenderMarketplaceMcpConfig precisely
		// so it names the offending key without quoting its value.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateWorkspaceMcpServerEntry(config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install the MCP server")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Same teardown fence as CreateWorkspaceMcpServer: the table has no FK, so
	// without the shared lock an install committing after DeleteWorkspace swept
	// would leave a row pointing at a workspace that no longer exists.
	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), workspaceUUID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	server, err := qtx.CreateWorkspaceMcpServer(r.Context(), db.CreateWorkspaceMcpServerParams{
		WorkspaceID: workspaceUUID,
		Name:        name,
		Config:      append([]byte(nil), config...),
		CreatedBy:   parseUUID(requestUserID(r)),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an MCP server with this name already exists in the workspace")
			return
		}
		// Never log err alongside the entry: the entry is where the token is.
		slog.Warn("marketplace mcp install failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", workspaceID, "item", item.Key)...)
		writeError(w, http.StatusInternalServerError, "failed to install the MCP server")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install the MCP server")
		return
	}
	// Name and catalog key only — never the rendered entry.
	slog.Info("marketplace mcp installed", append(logger.RequestAttrs(r),
		"workspace_id", workspaceID, "server_id", uuidToString(server.ID),
		"name", server.Name, "item", item.Key)...)
	writeJSON(w, http.StatusCreated, workspaceMcpServerToResponse(server))
}
