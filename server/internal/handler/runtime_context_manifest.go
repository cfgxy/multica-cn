package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RuntimeContextManifest describes server assembly and bundle responses only.
// It is not evidence that a client materialized, read, or executed any skill.
// No prompt body, environment, credential, or MCP configuration belongs here.
type RuntimeContextManifest struct {
	SchemaVersion int                         `json:"schema_version"`
	Evidence      string                      `json:"evidence"`
	TaskKind      string                      `json:"task_kind"`
	AssembledAt   string                      `json:"assembled_at"`
	Claim         *runtimeClaimGeneration     `json:"claim,omitempty"`
	Carriers      []runtimeCarrierEvidence    `json:"carriers"`
	Clauses       []runtimeClauseEvidence     `json:"clauses"`
	Observations  []runtimeCarrierObservation `json:"observations"`
	PayloadSHA256 map[string]string           `json:"payload_sha256"`
	Skills        runtimeSkillManifest        `json:"skills"`
	Rule          runtimeRuleEvidence         `json:"rule"`
}

// dispatched_at changes on every claim/reclaim; prepare leases may be renewed.
type runtimeClaimGeneration struct {
	RuntimeID    string `json:"runtime_id"`
	DispatchedAt string `json:"dispatched_at"`
}

func runtimeClaimForTask(task db.AgentTaskQueue) *runtimeClaimGeneration {
	if !task.RuntimeID.Valid || !task.DispatchedAt.Valid {
		return nil
	}
	return &runtimeClaimGeneration{
		RuntimeID: uuidToString(task.RuntimeID), DispatchedAt: task.DispatchedAt.Time.UTC().Format(time.RFC3339Nano),
	}
}

type runtimeCarrierEvidence struct {
	Scope          string `json:"scope"`
	ScopeID        string `json:"scope_id"`
	CarrierSHA256  string `json:"carrier_sha256"`
	InjectedSHA256 string `json:"injected_sha256"`
	VersionStatus  string `json:"version_status"`
	VersionID      string `json:"version_id,omitempty"`
	Version        *int32 `json:"version,omitempty"`
}

type runtimeClauseEvidence struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Version  int32  `json:"version"`
	Scope    string `json:"scope"`
	ScopeID  string `json:"scope_id"`
	SHA256   string `json:"sha256"`
	Degraded bool   `json:"degraded"`
}

type runtimeCarrierObservation struct {
	Scope    string `json:"scope"`
	ScopeID  string `json:"scope_id"`
	Status   string `json:"status"`
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
}

type runtimeSkillEvidence struct {
	ID        string                          `json:"id"`
	Source    string                          `json:"source"`
	Hash      string                          `json:"hash"`
	State     string                          `json:"state"`
	SizeBytes int64                           `json:"size_bytes"`
	FileCount int                             `json:"file_count"`
	Files     []service.AgentSkillFileRefData `json:"files,omitempty"`
}

type runtimeSkillResolution struct {
	runtimeSkillEvidence
	RequestedHash string `json:"requested_hash"`
	HashChanged   bool   `json:"hash_changed"`
	ResolvedAt    string `json:"resolved_at"`
}

type runtimeSkillManifest struct {
	Offered  []runtimeSkillEvidence            `json:"offered"`
	Resolved map[string]runtimeSkillResolution `json:"resolved"`
}

type runtimeRuleEvidence struct {
	VersionID string `json:"version_id,omitempty"`
	Status    string `json:"status"`
}

func promptTextSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func newRuntimeContextManifest(task *db.AgentTaskQueue, kind string) *RuntimeContextManifest {
	if kind == "" {
		kind = "unknown"
	}
	m := &RuntimeContextManifest{
		SchemaVersion: 1, Evidence: "server_assembled", TaskKind: kind,
		AssembledAt: time.Now().UTC().Format(time.RFC3339Nano),
		Claim:       runtimeClaimForTask(*task),
		Carriers:    []runtimeCarrierEvidence{}, Clauses: []runtimeClauseEvidence{},
		Observations: []runtimeCarrierObservation{}, PayloadSHA256: map[string]string{},
		Skills: runtimeSkillManifest{Offered: []runtimeSkillEvidence{}, Resolved: map[string]runtimeSkillResolution{}},
		Rule:   runtimeRuleEvidence{Status: "unknown"},
	}
	if task.RuleVersionID.Valid {
		m.Rule = runtimeRuleEvidence{VersionID: uuidToString(task.RuleVersionID), Status: "queue_version_ref"}
	}
	return m
}

// Match the bytes read for this carrier, never a later latest-version number.
// A dynamic clause render has a separate digest: carrier version attribution
// describes the stored carrier, not a claim that the render equals that row.
func (h *Handler) captureRuntimeCarrier(ctx context.Context, m *RuntimeContextManifest, scope string, scopeID pgtype.UUID, carrier string, result dynamicClauseResult) {
	id := uuidToString(scopeID)
	observation := runtimeCarrierObservation{Scope: scope, ScopeID: id, Status: "effective"}
	for _, degraded := range result.Observations {
		if degraded.Degraded {
			observation.Status, observation.Degraded, observation.Reason = "degraded", true, degraded.Reason
		}
	}
	m.Observations = append(m.Observations, observation)
	if strings.TrimSpace(result.Text) != "" {
		e := runtimeCarrierEvidence{Scope: scope, ScopeID: id, CarrierSHA256: promptTextSHA256(carrier), InjectedSHA256: promptTextSHA256(result.Text), VersionStatus: "unknown"}
		var version int32
		var versionID pgtype.UUID
		if h.DB != nil {
			err := h.DB.QueryRow(ctx, `SELECT id, version FROM prompt_version
				WHERE scope = $1 AND scope_id = $2 AND content = $3
				ORDER BY version DESC LIMIT 1`, scope, scopeID, carrier).Scan(&versionID, &version)
			switch {
			case err == nil:
				e.VersionStatus, e.VersionID, e.Version = "exact_carrier_content", uuidToString(versionID), &version
			case errors.Is(err, pgx.ErrNoRows):
				e.VersionStatus = "unmatched"
			}
		}
		m.Carriers = append(m.Carriers, e)
	}
	// Parse the actual returned render, so even a degraded snapshot's digest
	// identifies its actual body rather than the library's current body.
	doc, err := clausemark.Parse(result.Text)
	if err != nil {
		return
	}
	for i, entry := range result.Manifest {
		e := runtimeClauseEvidence{Slug: entry.Slug, Version: entry.Version, Scope: scope, ScopeID: id, Degraded: entry.Degraded}
		if i < len(result.Injections) {
			e.ID = uuidToString(result.Injections[i].ClauseID)
		}
		for _, segment := range doc.Segments {
			if segment.Slug == entry.Slug && segment.Rev == int(entry.Version) {
				e.SHA256 = promptTextSHA256(segment.Body)
				break
			}
		}
		m.Clauses = append(m.Clauses, e)
	}
}

func runtimeSkillRefEvidence(ref service.AgentSkillRefData, state string) runtimeSkillEvidence {
	return runtimeSkillEvidence{ID: ref.ID, Source: ref.Source, Hash: ref.Hash, State: state, SizeBytes: ref.SizeBytes, FileCount: ref.FileCount, Files: ref.Files}
}

func (m *RuntimeContextManifest) finish(resp AgentTaskResponse) {
	if resp.Agent != nil {
		for _, ref := range resp.Agent.SkillRefs {
			m.Skills.Offered = append(m.Skills.Offered, runtimeSkillRefEvidence(ref, "offered_ref"))
		}
		_, refs := service.BuildAgentSkillBundles(resp.Agent.Skills)
		for _, ref := range refs {
			m.Skills.Offered = append(m.Skills.Offered, runtimeSkillRefEvidence(ref, "offered_full_bundle"))
		}
		if resp.Agent.Instructions != "" {
			m.PayloadSHA256["agent.instructions"] = promptTextSHA256(resp.Agent.Instructions)
		}
	}
	for name, text := range map[string]string{"workspace_context": resp.WorkspaceContext, "project_instructions": resp.ProjectInstructions} {
		if text != "" {
			m.PayloadSHA256[name] = promptTextSHA256(text)
		}
	}
}

func (m *RuntimeContextManifest) promptVersions() map[string]int32 {
	versions := map[string]int32{}
	for _, carrier := range m.Carriers {
		if carrier.Version != nil {
			versions[carrier.Scope] = *carrier.Version
		}
	}
	return versions
}

// Merge into the live row under PostgreSQL's row lock, only while this exact
// claim is preparing. A late builder must neither replace newer evidence nor
// report success when its claim has expired or execution has started.
func (h *Handler) persistRuntimeContextManifest(ctx context.Context, task db.AgentTaskQueue, m *RuntimeContextManifest) error {
	manifest, err := json.Marshal(m)
	if err != nil {
		return err
	}
	versions, err := json.Marshal(m.promptVersions())
	if err != nil {
		return err
	}
	claim := runtimeClaimForTask(task)
	if m.Claim == nil || claim == nil || *m.Claim != *claim {
		return fmt.Errorf("persist runtime context manifest: %w", pgx.ErrNoRows)
	}
	tag, err := h.DB.Exec(ctx, `UPDATE agent_task_queue SET
		context = COALESCE(context, '{}'::jsonb) || jsonb_build_object('runtime_context_manifest', $2::jsonb),
		prompt_versions = $3::jsonb
		WHERE id = $1 AND runtime_id = $4 AND dispatched_at = $5
		AND status = 'dispatched' AND started_at IS NULL
		AND prepare_lease_expires_at > now()`, task.ID, manifest, versions, task.RuntimeID, task.DispatchedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("persist runtime context manifest: %w", pgx.ErrNoRows)
	}
	return nil
}

// Resolve evidence is keyed by identity AND both hashes, retaining each
// offered/requested -> returned transition across retries and concurrent calls.
// Historic tasks without claim-scoped evidence stay unchanged.
func (h *Handler) recordRuntimeSkillResolutions(ctx context.Context, task db.AgentTaskQueue, requested []resolveSkillBundleRef, bundles []service.AgentSkillData) error {
	m := runtimeContextManifestFromContext(task.Context)
	if m == nil || m.Claim == nil {
		return nil
	}
	claim := runtimeClaimForTask(task)
	if claim == nil || *claim != *m.Claim {
		return fmt.Errorf("record runtime skill resolutions: %w", pgx.ErrNoRows)
	}
	generation, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	_, refs := service.BuildAgentSkillBundles(bundles)
	resolved := map[string]runtimeSkillResolution{}
	for i, ref := range refs {
		entry := runtimeSkillResolution{runtimeSkillEvidence: runtimeSkillRefEvidence(ref, "resolved_bundle"), RequestedHash: requested[i].Hash, HashChanged: requested[i].Hash != ref.Hash, ResolvedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		key := promptTextSHA256(fmt.Sprintf("%s\x00%s\x00%s\x00%s", ref.Source, ref.ID, requested[i].Hash, ref.Hash))
		resolved[key] = entry
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return err
	}
	tag, err := h.DB.Exec(ctx, `UPDATE agent_task_queue SET context = jsonb_set(context,
		'{runtime_context_manifest,skills,resolved}',
		COALESCE(context #> '{runtime_context_manifest,skills,resolved}', '{}'::jsonb) || $2::jsonb)
		WHERE id = $1 AND runtime_id = $3 AND dispatched_at = $4
		AND status IN ('dispatched', 'waiting_local_directory') AND started_at IS NULL
		AND context #>> '{runtime_context_manifest,schema_version}' = '1'
		AND context #> '{runtime_context_manifest,claim}' = $5::jsonb`, task.ID, payload, task.RuntimeID, task.DispatchedAt, generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("record runtime skill resolutions: %w", pgx.ErrNoRows)
	}
	return nil
}

func runtimeContextManifestFromContext(raw []byte) *RuntimeContextManifest {
	var envelope struct {
		Manifest *RuntimeContextManifest `json:"runtime_context_manifest"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Manifest == nil || envelope.Manifest.SchemaVersion != 1 {
		return nil
	}
	return envelope.Manifest
}
