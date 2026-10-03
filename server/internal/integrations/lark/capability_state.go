package lark

// Persistence + sweep facade for the capability-probe verdicts
// (channel_capability_state, migration 922). The probe machinery lives in
// permission.go; this file is the only writer/reader of the table.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// CapabilityCheckResult is one capability's post-install probe verdict, in
// catalog (display) order.
type CapabilityCheckResult struct {
	Capability CapabilityID
	Status     ProbeStatus
	// Detail is the sanitized verdict line (business code + classification).
	Detail         string
	RequiredScopes []string
}

// CapabilityStateView is the stored-verdict shape the HTTP layer renders.
type CapabilityStateView struct {
	Capability     string    `json:"capability"`
	Status         string    `json:"status"`
	Detail         string    `json:"detail"`
	RequiredScopes []string  `json:"required_scopes"`
	CheckedAt      time.Time `json:"checked_at"`
}

// CheckInstallationCapabilities probes every catalog capability against
// creds and returns the verdicts in display order. The event-subscription
// capability is never probeable over REST and comes back unknown with its
// reason stated — the honest entry the UI renders as "以实际收发为准".
func CheckInstallationCapabilities(ctx context.Context, client APIClient, creds InstallationCredentials) []CapabilityCheckResult {
	out := make([]CapabilityCheckResult, 0, len(capabilityCatalog))
	for _, spec := range capabilityCatalog {
		outcome := ProbeCapability(ctx, client, creds, spec.ID)
		out = append(out, CapabilityCheckResult{
			Capability:     spec.ID,
			Status:         outcome.Status,
			Detail:         outcome.Detail,
			RequiredScopes: ScopesForCapability(spec.ID),
		})
	}
	return out
}

// SaveCapabilityStates persists probe verdicts, one upsert per capability —
// the table always carries the LATEST verdict per capability, never history.
// Errors bubble; the caller decides whether they are fatal (the recheck HTTP
// endpoint surfaces them) or best-effort (finishSuccess logs and continues).
func SaveCapabilityStates(ctx context.Context, q *db.Queries, installationID pgtype.UUID, results []CapabilityCheckResult) error {
	for _, r := range results {
		scopes, err := json.Marshal(r.RequiredScopes)
		if err != nil {
			return fmt.Errorf("marshal required scopes for %s: %w", r.Capability, err)
		}
		if err := q.UpsertChannelCapabilityState(ctx, db.UpsertChannelCapabilityStateParams{
			ID:             dbid.NewV7(),
			InstallationID: installationID,
			ChannelType:    channelTypeFeishu,
			Capability:     string(r.Capability),
			Status:         string(r.Status),
			Detail:         r.Detail,
			RequiredScopes: scopes,
		}); err != nil {
			return fmt.Errorf("upsert capability state %s: %w", r.Capability, err)
		}
	}
	return nil
}

// ListCapabilityStates reads the latest probe verdicts for an installation,
// capability-ordered for stable UI rendering.
func ListCapabilityStates(ctx context.Context, q *db.Queries, installationID pgtype.UUID) ([]CapabilityStateView, error) {
	rows, err := q.ListChannelCapabilityStates(ctx, installationID)
	if err != nil {
		return nil, err
	}
	out := make([]CapabilityStateView, 0, len(rows))
	for _, row := range rows {
		var scopes []string
		if len(row.RequiredScopes) > 0 {
			if err := json.Unmarshal(row.RequiredScopes, &scopes); err != nil {
				return nil, fmt.Errorf("unmarshal required scopes for %s: %w", row.Capability, err)
			}
		}
		out = append(out, CapabilityStateView{
			Capability:     row.Capability,
			Status:         row.Status,
			Detail:         row.Detail,
			RequiredScopes: scopes,
			CheckedAt:      row.CheckedAt.Time.UTC(),
		})
	}
	return out, nil
}
