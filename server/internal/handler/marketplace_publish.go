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
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/listingscan"
)

// The write half of the marketplace (RUYI-99): publish, update, withdraw.
//
// Read and install stay in marketplace.go under MarketplaceV1. This file is
// gated on the separate MarketplacePublishV1 flag (D5-A) so an operator can
// close the publish surface — the one with cross-workspace blast radius —
// without making the catalog unreadable or already-published entries
// uninstallable.

// requireMarketplacePublishV1 closes every publishing endpoint when the flag is
// off. It takes MarketplaceV1 as a prerequisite: publishing into a catalog
// nobody can read would produce listings with no way to verify them.
func (h *Handler) requireMarketplacePublishV1(w http.ResponseWriter, r *http.Request) bool {
	if !featureflags.MarketplaceV1Enabled(r.Context(), h.FeatureFlags) {
		writeError(w, http.StatusServiceUnavailable, "The marketplace is not enabled")
		return false
	}
	if !featureflags.MarketplacePublishV1Enabled(r.Context(), h.FeatureFlags) {
		writeError(w, http.StatusServiceUnavailable, "Marketplace publishing is not enabled")
		return false
	}
	return true
}

// MarketplaceListingResponse is one listing as its OWN workspace sees it, in
// the "published from here" management view.
//
// It carries the MCP template because the publisher authored it and needs it
// back to edit — the template holds `${placeholder}` tokens, never values, so
// this is not a path back to any workspace's stored credentials. It does NOT
// carry source_workspace_id: even to its owner that field is redundant, and
// keeping it out of the type means no future handler can leak it by reusing
// this struct on a cross-workspace route.
type MarketplaceListingResponse struct {
	ID                   string   `json:"id"`
	Key                  string   `json:"key"`
	Kind                 string   `json:"kind"`
	Name                 string   `json:"name"`
	PublisherDisplayName string   `json:"publisher_display_name"`
	Summary              string   `json:"summary"`
	Description          string   `json:"description"`
	HomepageURL          string   `json:"homepage_url"`
	Categories           []string `json:"categories"`
	SourceURL            string   `json:"source_url,omitempty"`

	// ConfigTemplate is the raw MCP entry template; Transport is its
	// classification, so the management list can label a row without the client
	// having to re-implement mcpTransportOf.
	ConfigTemplate json.RawMessage                  `json:"config_template,omitempty"`
	Transport      string                           `json:"transport,omitempty"`
	Placeholders   []MarketplacePlaceholderResponse `json:"placeholders,omitempty"`

	State string `json:"state"`
	// Revision is the optimistic-concurrency token. A client must send back
	// the value it read; a stale one is refused with 409.
	Revision    int32  `json:"revision"`
	PublishedAt string `json:"published_at,omitempty"`
	WithdrawnAt string `json:"withdrawn_at,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// MarketplaceScanFindingResponse is one secret-scan finding as the publish
// error returns it. Mirrors listingscan.Finding exactly — category, rule, field
// and line, never the matched text.
type MarketplaceScanFindingResponse struct {
	Category string `json:"category"`
	Rule     string `json:"rule"`
	Field    string `json:"field"`
	Line     int    `json:"line"`
	Mask     string `json:"mask"`
}

// MarketplaceScanErrorResponse is the 422 body of a blocked publish.
type MarketplaceScanErrorResponse struct {
	Error           string                           `json:"error"`
	ScannerRevision string                           `json:"scanner_revision"`
	Findings        []MarketplaceScanFindingResponse `json:"findings"`
	Truncated       bool                             `json:"truncated"`
}

// MarketplacePublishRequest is what a publish or update submits.
//
// There is no `values` field and no reference to an installed MCP server: the
// template is authored here, and workspace_mcp_server.config is never read to
// build it. That is the write-only boundary, kept intact by not having a way to
// cross it rather than by a check that could be forgotten.
type MarketplacePublishRequest struct {
	Kind           string                        `json:"kind"`
	Name           string                        `json:"name"`
	Summary        string                        `json:"summary"`
	Description    string                        `json:"description"`
	HomepageURL    string                        `json:"homepage_url"`
	Categories     []string                      `json:"categories"`
	SourceURL      string                        `json:"source_url"`
	ConfigTemplate json.RawMessage               `json:"config_template"`
	Placeholders   []MarketplacePlaceholderInput `json:"placeholders"`

	// Revision is required on update and withdraw and ignored on publish.
	Revision int32 `json:"revision"`
}

// MarketplacePlaceholderInput is one declared placeholder. `secret` only drives
// masking in the install dialog; it grants no storage difference, because the
// value never reaches this table in any case.
type MarketplacePlaceholderInput struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

// marketplaceListingToResponse projects a row for its own workspace.
func marketplaceListingToResponse(row db.MarketplaceListing) MarketplaceListingResponse {
	resp := MarketplaceListingResponse{
		ID:                   uuidToString(row.ID),
		Key:                  service.MarketplaceListingKey(uuidToString(row.ID)),
		Kind:                 row.Kind,
		Name:                 row.Name,
		PublisherDisplayName: row.PublisherDisplayName,
		Summary:              row.Summary,
		Description:          row.Description,
		HomepageURL:          row.HomepageUrl,
		Categories:           decodeMarketplaceCategories(row.Categories),
		SourceURL:            row.SourceUrl,
		Placeholders:         marketplacePlaceholders(marketplaceItemFromListing(row)),
		State:                row.State,
		Revision:             row.Revision,
		PublishedAt:          timestampToString(row.PublishedAt),
		WithdrawnAt:          timestampToString(row.WithdrawnAt),
		CreatedAt:            timestampToString(row.CreatedAt),
		UpdatedAt:            timestampToString(row.UpdatedAt),
	}
	if row.Kind == service.MarketplaceKindMcp && len(row.ConfigTemplate) > 0 {
		resp.ConfigTemplate = json.RawMessage(row.ConfigTemplate)
		resp.Transport = mcpTransportOf(json.RawMessage(row.ConfigTemplate))
	}
	return resp
}

// decodeMarketplaceCategories reads the JSONB array. A row that somehow holds a
// non-array reads as no categories rather than failing the whole listing: the
// column has a default and the write path always encodes an array, so this is a
// tolerated-corruption path, not a validation one.
func decodeMarketplaceCategories(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func decodeMarketplacePlaceholders(raw []byte) []service.MarketplacePlaceholder {
	if len(raw) == 0 {
		return nil
	}
	var out []service.MarketplacePlaceholder
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// marketplaceItemFromListing projects a row into the shared catalog item type,
// which is what the merged listing, the install path and the placeholder
// response all consume.
func marketplaceItemFromListing(row db.MarketplaceListing) service.MarketplaceItem {
	listing := service.PublishedMarketplaceListing{
		ID:                   uuidToString(row.ID),
		Kind:                 row.Kind,
		Name:                 row.Name,
		PublisherDisplayName: row.PublisherDisplayName,
		Summary:              row.Summary,
		Description:          row.Description,
		HomepageURL:          row.HomepageUrl,
		Categories:           decodeMarketplaceCategories(row.Categories),
		SourceURL:            row.SourceUrl,
		Placeholders:         decodeMarketplacePlaceholders(row.Placeholders),
	}
	if row.Kind == service.MarketplaceKindMcp {
		listing.ConfigTemplate = json.RawMessage(row.ConfigTemplate)
	}
	return listing.ToMarketplaceItem()
}

// marketplaceDraftFromRequest turns a request into the catalog item shape the
// validator and the scanner both work on, so there is one normalisation of the
// submitted fields rather than three.
func marketplaceDraftFromRequest(req MarketplacePublishRequest) service.MarketplaceItem {
	categories := make([]string, 0, len(req.Categories))
	for _, category := range req.Categories {
		if trimmed := strings.TrimSpace(category); trimmed != "" {
			categories = append(categories, trimmed)
		}
	}
	item := service.MarketplaceItem{
		Kind:        strings.TrimSpace(req.Kind),
		Name:        strings.TrimSpace(req.Name),
		Summary:     strings.TrimSpace(req.Summary),
		Description: strings.TrimSpace(req.Description),
		HomepageURL: strings.TrimSpace(req.HomepageURL),
		Categories:  categories,
	}
	switch item.Kind {
	case service.MarketplaceKindSkill:
		// A skill listing carries only its public source; a template submitted
		// alongside it is dropped rather than stored, so a client cannot smuggle
		// an MCP payload into a skill row.
		item.SourceURL = strings.TrimSpace(req.SourceURL)
	case service.MarketplaceKindMcp:
		item.ConfigTemplate = req.ConfigTemplate
		for _, p := range req.Placeholders {
			item.Placeholders = append(item.Placeholders, service.MarketplacePlaceholder{
				Key:         strings.TrimSpace(p.Key),
				Label:       strings.TrimSpace(p.Label),
				Description: strings.TrimSpace(p.Description),
				Secret:      p.Secret,
				Required:    p.Required,
			})
		}
	}
	return item
}

// scanMarketplaceDraft runs the fail-closed secret gate and writes the 422 when
// it blocks. Returns the result so the caller can persist the revision that
// cleared the content.
func scanMarketplaceDraft(w http.ResponseWriter, r *http.Request, item service.MarketplaceItem) (listingscan.Result, bool) {
	// The whole placeholder goes to the scanner, not just the key: label and
	// description are published verbatim and are as good a hiding place for a
	// credential as the description field.
	declared := make([]listingscan.Placeholder, 0, len(item.Placeholders))
	for _, p := range item.Placeholders {
		declared = append(declared, listingscan.Placeholder{
			Key:         p.Key,
			Label:       p.Label,
			Description: p.Description,
		})
	}
	res := listingscan.Scan(listingscan.Input{
		Name:           item.Name,
		Summary:        item.Summary,
		Description:    item.Description,
		HomepageURL:    item.HomepageURL,
		Categories:     item.Categories,
		SourceURL:      item.SourceURL,
		ConfigTemplate: item.ConfigTemplate,
		Placeholders:   declared,
	})
	if res.OK() {
		return res, true
	}
	findings := make([]MarketplaceScanFindingResponse, 0, len(res.Findings))
	for _, f := range res.Findings {
		findings = append(findings, MarketplaceScanFindingResponse{
			Category: string(f.Category),
			Rule:     f.Rule,
			Field:    f.Field,
			Line:     f.Line,
			Mask:     f.Mask,
		})
	}
	// Rule and field only — the request body is where the token is, so it is
	// never logged, and the findings carry no matched text by construction.
	slog.Warn("marketplace publish blocked by secret scan", append(logger.RequestAttrs(r),
		"scanner_revision", res.Revision, "findings", len(res.Findings))...)
	writeJSON(w, http.StatusUnprocessableEntity, MarketplaceScanErrorResponse{
		Error:           "the listing looks like it contains a credential; remove it and publish again",
		ScannerRevision: res.Revision,
		Findings:        findings,
		Truncated:       res.Truncated,
	})
	return res, false
}

// encodeMarketplaceListingColumns serialises the JSONB columns. An encode
// failure here is a bug rather than user input — every value came out of a
// validated struct — so the caller reports it as a 500 without echoing the
// payload.
func encodeMarketplaceListingColumns(item service.MarketplaceItem, scan listingscan.Result) (categories, placeholders, template, scanResult []byte, err error) {
	if item.Categories == nil {
		item.Categories = []string{}
	}
	if categories, err = json.Marshal(item.Categories); err != nil {
		return nil, nil, nil, nil, err
	}
	declared := item.Placeholders
	if declared == nil {
		declared = []service.MarketplacePlaceholder{}
	}
	if placeholders, err = json.Marshal(declared); err != nil {
		return nil, nil, nil, nil, err
	}
	template = []byte("{}")
	if item.Kind == service.MarketplaceKindMcp && len(item.ConfigTemplate) > 0 {
		// Re-marshal through a map so the stored template is canonical JSON
		// rather than whatever whitespace the client sent.
		var entry map[string]json.RawMessage
		if err = json.Unmarshal(item.ConfigTemplate, &entry); err != nil {
			return nil, nil, nil, nil, err
		}
		if template, err = json.Marshal(entry); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	findings := scan.Findings
	if findings == nil {
		findings = []listingscan.Finding{}
	}
	if scanResult, err = json.Marshal(findings); err != nil {
		return nil, nil, nil, nil, err
	}
	return categories, placeholders, template, scanResult, nil
}

// requireMarketplacePublisher is the publish-side authorisation gate.
//
// It is the SAME gate the MCP library write path takes — a human owner or admin
// of the workspace, never an agent actor — because publishing puts a workspace's
// name on content every other workspace can install. An agent running under an
// owner's token is refused: an autonomous publish is a cross-workspace action
// nobody explicitly approved.
func (h *Handler) requireMarketplacePublisher(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	return h.requireWorkspaceMcpWriter(w, r, workspaceID)
}

// ListMarketplaceListings returns what THIS workspace has published, tombstones
// included — a withdrawn row is what a republication acts on, so hiding it
// would make the name it still reserves invisible to the only workspace allowed
// to reuse it.
func (h *Handler) ListMarketplaceListings(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplacePublishV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	// Reading your own publications is a management view of workspace-owned
	// content, so it takes the writer gate rather than plain membership.
	if !h.requireMarketplacePublisher(w, r, workspaceID) {
		return
	}
	rows, err := h.Queries.ListMarketplaceListingsBySourceWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the workspace's marketplace listings")
		return
	}
	resp := make([]MarketplaceListingResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, marketplaceListingToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

// PublishMarketplaceListing publishes a new listing from this workspace.
//
// D3-A: (kind, lowercased name) is globally unique and stays reserved after
// withdrawal. So this endpoint does double duty — publishing a name whose
// tombstone belongs to THIS workspace republishes it, and one belonging to
// another workspace is refused. Both outcomes are reached through the same
// locked read, so there is no window where the check and the write disagree.
func (h *Handler) PublishMarketplaceListing(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplacePublishV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireMarketplacePublisher(w, r, workspaceID) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req MarketplacePublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	item := marketplaceDraftFromRequest(req)
	if err := service.ValidateMarketplaceListingDraft(item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nameKey := service.NormalizeMarketplaceName(item.Name)
	if taken, err := marketplaceNameTakenByCatalog(item.Kind, nameKey); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the marketplace catalog")
		return
	} else if taken {
		writeError(w, http.StatusConflict, "this name is already used by a built-in marketplace entry")
		return
	}
	scan, ok := scanMarketplaceDraft(w, r, item)
	if !ok {
		return
	}
	categories, placeholders, template, scanResult, err := encodeMarketplaceListingColumns(item, scan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish the listing")
		return
	}

	publisherName := h.marketplacePublisherName(r, workspaceUUID)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish the listing")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Same teardown fence the MCP install takes: the table has no FK, so
	// without the shared lock a publish committing after DeleteWorkspace swept
	// would leave a listing attributed to a workspace that no longer exists.
	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), workspaceUUID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Locked conflict probe. Two concurrent publishes of the same name
	// serialise here; the unique index is still the second line of defence.
	existing, err := qtx.GetMarketplaceListingByNameKeyForUpdate(r.Context(), db.GetMarketplaceListingByNameKeyForUpdateParams{
		Kind:    item.Kind,
		NameKey: nameKey,
	})
	switch {
	case err == nil:
		// The name exists. Only a tombstone owned by this workspace may be
		// revived; anything else is a conflict, and the message deliberately
		// does not say which workspace holds it.
		if existing.State != "withdrawn" {
			writeError(w, http.StatusConflict, "a marketplace listing with this name already exists")
			return
		}
		if uuidToString(existing.SourceWorkspaceID) != uuidToString(workspaceUUID) {
			writeError(w, http.StatusConflict, "a marketplace listing with this name already exists")
			return
		}
		revived, err := qtx.RepublishMarketplaceListing(r.Context(), db.RepublishMarketplaceListingParams{
			ID:                   existing.ID,
			Name:                 item.Name,
			Summary:              item.Summary,
			Description:          item.Description,
			HomepageUrl:          item.HomepageURL,
			Categories:           categories,
			SourceUrl:            item.SourceURL,
			ConfigTemplate:       template,
			Placeholders:         placeholders,
			PublisherUserID:      parseUUID(userID),
			PublisherDisplayName: publisherName,
			ScannerRevision:      scan.Revision,
			ScanResult:           scanResult,
		})
		if err != nil {
			slog.Warn("marketplace listing republish failed", append(logger.RequestAttrs(r),
				"error", err, "workspace_id", workspaceID, "listing_id", uuidToString(existing.ID))...)
			writeError(w, http.StatusInternalServerError, "failed to publish the listing")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to publish the listing")
			return
		}
		slog.Info("marketplace listing republished", append(logger.RequestAttrs(r),
			"workspace_id", workspaceID, "listing_id", uuidToString(revived.ID),
			"kind", revived.Kind, "name", revived.Name)...)
		writeJSON(w, http.StatusOK, marketplaceListingToResponse(revived))
		return
	case errors.Is(err, pgx.ErrNoRows):
		// The ordinary path: the name is free.
	default:
		writeError(w, http.StatusInternalServerError, "failed to publish the listing")
		return
	}

	created, err := qtx.CreateMarketplaceListing(r.Context(), db.CreateMarketplaceListingParams{
		Kind:                 item.Kind,
		Name:                 item.Name,
		NameKey:              nameKey,
		SourceWorkspaceID:    workspaceUUID,
		PublisherUserID:      parseUUID(userID),
		PublisherDisplayName: publisherName,
		Summary:              item.Summary,
		Description:          item.Description,
		HomepageUrl:          item.HomepageURL,
		Categories:           categories,
		SourceUrl:            item.SourceURL,
		ConfigTemplate:       template,
		Placeholders:         placeholders,
		ScannerRevision:      scan.Revision,
		ScanResult:           scanResult,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a marketplace listing with this name already exists")
			return
		}
		// Never log err alongside the submitted template.
		slog.Warn("marketplace listing publish failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", workspaceID, "kind", item.Kind)...)
		writeError(w, http.StatusInternalServerError, "failed to publish the listing")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish the listing")
		return
	}
	slog.Info("marketplace listing published", append(logger.RequestAttrs(r),
		"workspace_id", workspaceID, "listing_id", uuidToString(created.ID),
		"kind", created.Kind, "name", created.Name)...)
	writeJSON(w, http.StatusCreated, marketplaceListingToResponse(created))
}

// UpdateMarketplaceListing rewrites a published listing in place.
//
// Scope excludes semver and version history, so an update is a rewrite rather
// than a new version, and `revision` is the optimistic-concurrency token that
// keeps two editors from silently overwriting each other. The content is
// re-scanned: an update is exactly how a credential would be introduced into a
// listing that once passed.
func (h *Handler) UpdateMarketplaceListing(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplacePublishV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireMarketplacePublisher(w, r, workspaceID) {
		return
	}
	listingID := chi.URLParam(r, "id")
	listingUUID, ok := parseUUIDOrBadRequest(w, listingID, "listing id")
	if !ok {
		return
	}

	var req MarketplacePublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Revision <= 0 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update the listing")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	current, ok := h.lockOwnedMarketplaceListing(w, r, qtx, listingUUID, workspaceUUID)
	if !ok {
		return
	}
	if current.State != "published" {
		writeError(w, http.StatusConflict, "a withdrawn listing cannot be edited; publish it again instead")
		return
	}

	// The kind is fixed at publish time: changing it would turn an installed
	// skill's provenance into an MCP entry's, and the request cannot be trusted
	// to say which it was.
	req.Kind = current.Kind
	item := marketplaceDraftFromRequest(req)
	if err := service.ValidateMarketplaceListingDraft(item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nameKey := service.NormalizeMarketplaceName(item.Name)
	if nameKey != current.NameKey {
		// A rename re-enters the global name space and must clear the same
		// gates a fresh publish does.
		if taken, err := marketplaceNameTakenByCatalog(item.Kind, nameKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load the marketplace catalog")
			return
		} else if taken {
			writeError(w, http.StatusConflict, "this name is already used by a built-in marketplace entry")
			return
		}
		if _, err := qtx.GetMarketplaceListingByNameKeyForUpdate(r.Context(), db.GetMarketplaceListingByNameKeyForUpdateParams{
			Kind:    item.Kind,
			NameKey: nameKey,
		}); err == nil {
			writeError(w, http.StatusConflict, "a marketplace listing with this name already exists")
			return
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to update the listing")
			return
		}
	}

	scan, ok := scanMarketplaceDraft(w, r, item)
	if !ok {
		return
	}
	categories, placeholders, template, scanResult, err := encodeMarketplaceListingColumns(item, scan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update the listing")
		return
	}

	updated, err := qtx.UpdateMarketplaceListing(r.Context(), db.UpdateMarketplaceListingParams{
		ID:              listingUUID,
		Revision:        req.Revision,
		Name:            item.Name,
		NameKey:         nameKey,
		Summary:         item.Summary,
		Description:     item.Description,
		HomepageUrl:     item.HomepageURL,
		Categories:      categories,
		SourceUrl:       item.SourceURL,
		ConfigTemplate:  template,
		Placeholders:    placeholders,
		ScannerRevision: scan.Revision,
		ScanResult:      scanResult,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The guard did not match. The row is locked and was published a
			// moment ago, so the revision is what changed.
			writeError(w, http.StatusConflict, "this listing was changed by someone else; reload it and try again")
			return
		}
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a marketplace listing with this name already exists")
			return
		}
		slog.Warn("marketplace listing update failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", workspaceID, "listing_id", listingID)...)
		writeError(w, http.StatusInternalServerError, "failed to update the listing")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update the listing")
		return
	}
	slog.Info("marketplace listing updated", append(logger.RequestAttrs(r),
		"workspace_id", workspaceID, "listing_id", listingID, "revision", updated.Revision)...)
	writeJSON(w, http.StatusOK, marketplaceListingToResponse(updated))
}

// WithdrawMarketplaceListing tombstones a listing.
//
// D4-A: this does not touch anything already installed. An installed skill is
// an ordinary workspace skill and an installed MCP entry is an ordinary library
// row; neither holds a pointer back here, so withdrawal is purely a catalog
// action. That is a property of the data model rather than a rule this handler
// enforces, which is why there is no sweep here to get wrong.
func (h *Handler) WithdrawMarketplaceListing(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplacePublishV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireMarketplacePublisher(w, r, workspaceID) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	listingID := chi.URLParam(r, "id")
	listingUUID, ok := parseUUIDOrBadRequest(w, listingID, "listing id")
	if !ok {
		return
	}
	var req MarketplacePublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Revision <= 0 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to withdraw the listing")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	current, ok := h.lockOwnedMarketplaceListing(w, r, qtx, listingUUID, workspaceUUID)
	if !ok {
		return
	}
	if current.State == "withdrawn" {
		// A repeat withdrawal is refused rather than absorbed: silently
		// succeeding would re-stamp withdrawn_at and lose who withdrew it.
		writeError(w, http.StatusConflict, "this listing is already withdrawn")
		return
	}

	withdrawn, err := qtx.WithdrawMarketplaceListing(r.Context(), db.WithdrawMarketplaceListingParams{
		ID:          listingUUID,
		Revision:    req.Revision,
		WithdrawnBy: parseUUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "this listing was changed by someone else; reload it and try again")
			return
		}
		slog.Warn("marketplace listing withdraw failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", workspaceID, "listing_id", listingID)...)
		writeError(w, http.StatusInternalServerError, "failed to withdraw the listing")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to withdraw the listing")
		return
	}
	slog.Info("marketplace listing withdrawn", append(logger.RequestAttrs(r),
		"workspace_id", workspaceID, "listing_id", listingID)...)
	writeJSON(w, http.StatusOK, marketplaceListingToResponse(withdrawn))
}

// lockOwnedMarketplaceListing reads a listing for update and checks that this
// workspace owns it. A listing owned by someone else answers 404 rather than
// 403: the caller has no business knowing the id exists, and a 403 would turn
// this endpoint into an oracle for probing other workspaces' listing ids.
func (h *Handler) lockOwnedMarketplaceListing(w http.ResponseWriter, r *http.Request, qtx *db.Queries, listingUUID, workspaceUUID pgtype.UUID) (db.MarketplaceListing, bool) {
	row, err := qtx.GetMarketplaceListingForUpdate(r.Context(), listingUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "marketplace listing not found")
			return db.MarketplaceListing{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load the listing")
		return db.MarketplaceListing{}, false
	}
	if uuidToString(row.SourceWorkspaceID) != uuidToString(workspaceUUID) {
		writeError(w, http.StatusNotFound, "marketplace listing not found")
		return db.MarketplaceListing{}, false
	}
	return row, true
}

// marketplaceNameTakenByCatalog reports whether the embedded catalog already
// occupies this (kind, name).
func marketplaceNameTakenByCatalog(kind, nameKey string) (bool, error) {
	occupied, err := service.StaticMarketplaceNameKeys()
	if err != nil {
		return false, err
	}
	_, taken := occupied[kind+"/"+nameKey]
	return taken, nil
}

// marketplacePublisherName is the attribution the catalog shows. It is the
// WORKSPACE name, not the publishing user's: the listing is workspace-owned
// content, and putting an individual's name on an entry every workspace can
// read would publish org membership as a side effect of publishing a skill.
//
// A workspace that cannot be read falls back to empty attribution rather than
// failing the publish — the caller's membership was already verified, so this
// is a display concern.
func (h *Handler) marketplacePublisherName(r *http.Request, workspaceUUID pgtype.UUID) string {
	workspace, err := h.Queries.GetWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		return ""
	}
	return workspace.Name
}
