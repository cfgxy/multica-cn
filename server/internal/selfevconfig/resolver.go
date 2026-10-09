// Package selfevconfig is the per-workspace model-service configuration for
// the self-evolution module (RUYI-551): one resolver that every module LLM
// consumer (quality scoring, the daily retrospective) asks "which client do
// I use in THIS workspace?".
//
// The priority chain is the module's contract:
//
//	module config (self_evolution_model_config row)
//	  > deploy-injected default (MULTICA_LLM_*, the process-wide client)
//	  > unconfigured (nil client)
//
// The API key is stored as secretbox ciphertext (same at-rest construction
// as runtime credentials); it is decrypted only here, only to build an
// llm.Client, and never echoed — not in responses, not in logs, not in
// persisted validation messages (maskSecrets). Saving credentials without a
// passing validation is refused: a green status card can only mean the
// gateway answered a real ping.
package selfevconfig

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/llm"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// Source labels for the priority chain. Frontend and run records carry them
// verbatim.
const (
	SourceModuleConfig  = "module_config"
	SourceDeployDefault = "deploy_default"
	SourceNone          = "none"
)

// Scoring-state statuses for the four-state closed loop (design §2.5).
const (
	StatusOK           = "ok"
	StatusUnconfigured = "unconfigured"
	StatusError        = "error"
	StatusDisabled     = "disabled"
)

const (
	// validationTimeout bounds the mandatory save/revalidate ping.
	validationTimeout = 10 * time.Second
	// cacheTTL bounds how long a Resolve may ride without re-reading the
	// row. Writes invalidate immediately; the TTL is only a safety net for
	// out-of-band changes.
	cacheTTL = 30 * time.Second
	// maxValidationMessage keeps a hostile gateway's error body from
	// ballooning the config row.
	maxValidationMessage = 300
)

// Store is the slice of sqlc the resolver consumes. *db.Queries satisfies it.
type Store interface {
	GetSelfEvolutionModelConfig(ctx context.Context, workspaceID pgtype.UUID) (db.SelfEvolutionModelConfig, error)
	UpsertSelfEvolutionModelConfig(ctx context.Context, arg db.UpsertSelfEvolutionModelConfigParams) (db.SelfEvolutionModelConfig, error)
	UpdateSelfEvolutionModelConfigValidation(ctx context.Context, arg db.UpdateSelfEvolutionModelConfigValidationParams) error
	DeleteSelfEvolutionModelConfig(ctx context.Context, workspaceID pgtype.UUID) error
}

// RunTarget is what a module consumer needs per workspace: the client to
// call, where it came from, the model to request, and the workspace scoring
// switch. Client is nil when Source == none.
type RunTarget struct {
	Client         *llm.Client
	Source         string
	Model          string
	ScoringEnabled bool
}

// Resolver resolves the priority chain per workspace. Safe for concurrent
// use.
type Resolver struct {
	store       Store
	box         *secretbox.Box
	deploy      *llm.Client
	deployModel string
	now         func() time.Time

	mu    sync.Mutex
	cache map[pgtype.UUID]cacheEntry
}

type cacheEntry struct {
	target  RunTarget
	expires time.Time
}

// NewResolver wires the resolver. deploy (and deployModel) describe the
// process-wide default built in main from MULTICA_LLM_*; either may be a
// disabled client, which simply keeps the chain falling through to none.
func NewResolver(store Store, box *secretbox.Box, deploy *llm.Client, deployModel string) *Resolver {
	return &Resolver{
		store:       store,
		box:         box,
		deploy:      deploy,
		deployModel: deployModel,
		now:         time.Now,
		cache:       map[pgtype.UUID]cacheEntry{},
	}
}

// ResolveRunTarget walks the priority chain (cached; see cacheTTL).
func (r *Resolver) ResolveRunTarget(ctx context.Context, workspaceID pgtype.UUID) (RunTarget, error) {
	if e, ok := r.cached(workspaceID); ok {
		return e, nil
	}
	row, err := r.store.GetSelfEvolutionModelConfig(ctx, workspaceID)
	if err != nil && !isNoRows(err) {
		return RunTarget{}, err
	}
	target := RunTarget{}
	hasRow := err == nil
	if hasRow {
		target.ScoringEnabled = row.ScoringEnabled
	}
	if hasRow && rowCredsConfigured(row) {
		client, err := r.buildClient(row)
		if err != nil {
			return RunTarget{}, err
		}
		target.Client = client
		target.Source = SourceModuleConfig
		target.Model = row.Model.String
	} else if r.deploy.Enabled() {
		target.Client = r.deploy
		target.Source = SourceDeployDefault
		target.Model = r.deployModel
		if target.Model == "" && r.deploy.Enabled() {
			target.Model = r.deploy.DefaultModel()
		}
	} else {
		target.Source = SourceNone
	}
	r.storeCache(workspaceID, target)
	return target, nil
}

// ScoringEnabled reports the workspace scoring switch (module default true).
func (r *Resolver) ScoringEnabled(ctx context.Context, workspaceID pgtype.UUID) (bool, error) {
	t, err := r.ResolveRunTarget(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	return t.ScoringEnabled, nil
}

// State is the module-config view the status cards render.
type State struct {
	Status          string // ok | unconfigured | error | disabled
	EffectiveSource string // module_config | deploy_default | "" when unconfigured
	Model           string
	LastValidatedAt pgtype.Timestamptz
	LastValidationOK pgtype.Bool
	ValidationError string
}

// ScoringState derives the four-state closed loop for the quality/status
// card: switch off → disabled (even over a healthy config); a validated
// module config → ok/error per its recorded outcome; otherwise the deploy
// default → ok with no validation data (Plan A: the env source never claims
// 配置错误, it simply never promised a check); nothing → unconfigured.
func (r *Resolver) ScoringState(ctx context.Context, workspaceID pgtype.UUID) (State, error) {
	row, err := r.store.GetSelfEvolutionModelConfig(ctx, workspaceID)
	if err != nil && !isNoRows(err) {
		return State{}, err
	}
	hasRow := err == nil

	if hasRow {
		st := State{
			LastValidatedAt:  row.LastValidatedAt,
			LastValidationOK: row.LastValidationOk,
			ValidationError:  row.LastValidationError,
		}
		if rowCredsConfigured(row) {
			st.EffectiveSource = SourceModuleConfig
			st.Model = row.Model.String
		}
		if !row.ScoringEnabled {
			st.Status = StatusDisabled
			if st.EffectiveSource == "" && r.deploy.Enabled() {
				st.EffectiveSource = SourceDeployDefault
				st.Model = r.deployModel
			}
			return st, nil
		}
		if st.EffectiveSource == SourceModuleConfig {
			if row.LastValidationOk.Valid && !row.LastValidationOk.Bool {
				st.Status = StatusError
				return st, nil
			}
			st.Status = StatusOK
			return st, nil
		}
		// switch-only row: fall through to the deploy source
		if r.deploy.Enabled() {
			return State{Status: StatusOK, EffectiveSource: SourceDeployDefault, Model: r.deployModel}, nil
		}
		return State{Status: StatusUnconfigured}, nil
	}

	if r.deploy.Enabled() {
		return State{Status: StatusOK, EffectiveSource: SourceDeployDefault, Model: r.deployModel}, nil
	}
	return State{Status: StatusUnconfigured}, nil
}

// SaveInput is a config-card save. APIKey empty means "keep the stored key"
// when one exists. BaseURL+Model empty means a switch-only save (no
// credentials, no validation).
type SaveInput struct {
	BaseURL        string
	APIKey         string
	Model          string
	ScoringEnabled bool
}

// ValidationFailure is why a save was refused. Message is masked and
// user-displayable.
type ValidationFailure struct {
	Kind    llm.ErrorKind
	Message string
}

// ValidationOutcome is one validation pass's result.
type ValidationOutcome struct {
	OK          bool
	Kind        llm.ErrorKind
	Message     string
	ValidatedAt time.Time
}

// Save validates (mandatory whenever credentials are present) and only then
// persists, returning the stored row. A failed validation persists nothing.
func (r *Resolver) Save(ctx context.Context, workspaceID pgtype.UUID, in SaveInput) (db.SelfEvolutionModelConfig, *ValidationFailure, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.Model = strings.TrimSpace(in.Model)
	in.APIKey = strings.TrimSpace(in.APIKey)

	var zero db.SelfEvolutionModelConfig
	creds := in.BaseURL != "" || in.Model != ""
	if creds && (in.BaseURL == "" || in.Model == "") {
		return zero, nil, ErrIncompleteCreds
	}

	var apiKey []byte
	if creds {
		stored, err := r.store.GetSelfEvolutionModelConfig(ctx, workspaceID)
		if err != nil && !isNoRows(err) {
			return zero, nil, err
		}
		plainKey := in.APIKey
		if plainKey == "" && err == nil && len(stored.ApiKeyEncrypted) > 0 {
			plain, err := r.box.Open(stored.ApiKeyEncrypted)
			if err != nil {
				return zero, nil, err
			}
			plainKey = string(plain)
		}
		if plainKey != "" {
			sealed, err := r.box.Seal([]byte(plainKey))
			if err != nil {
				return zero, nil, err
			}
			apiKey = sealed
		}

		outcome := r.validate(ctx, in.BaseURL, plainKeyIf(apiKey, r), in.Model)
		if !outcome.OK {
			return zero, &ValidationFailure{Kind: outcome.Kind, Message: outcome.Message}, nil
		}
	}

	params := db.UpsertSelfEvolutionModelConfigParams{
		WorkspaceID:    workspaceID,
		ScoringEnabled: in.ScoringEnabled,
	}
	if creds {
		params.BaseUrl = pgtype.Text{String: in.BaseURL, Valid: true}
		params.Model = pgtype.Text{String: in.Model, Valid: true}
		params.ApiKeyEncrypted = apiKey
		params.LastValidatedAt = pgtype.Timestamptz{Time: r.now(), Valid: true}
		params.LastValidationOk = pgtype.Bool{Bool: true, Valid: true}
	}
	row, err := r.store.UpsertSelfEvolutionModelConfig(ctx, params)
	if err != nil {
		return zero, nil, err
	}
	r.invalidate(workspaceID)
	return row, nil, nil
}

// ValidateOnly runs a validation pass for the given input without touching
// the store (pre-save preview from the config card).
func (r *Resolver) ValidateOnly(ctx context.Context, workspaceID pgtype.UUID, in SaveInput) (ValidationOutcome, error) {
	// The workspace id is unused today; it keeps the signature ready for
	// per-workspace rate limits without a breaking change.
	_ = workspaceID
	return r.validate(ctx, strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.APIKey), strings.TrimSpace(in.Model)), nil
}

// RevalidateStored re-runs validation against the stored override and
// persists the outcome (the status card's 重新校验). A workspace without an
// override validates to not-ok without writing anything.
func (r *Resolver) RevalidateStored(ctx context.Context, workspaceID pgtype.UUID) (ValidationOutcome, error) {
	row, err := r.store.GetSelfEvolutionModelConfig(ctx, workspaceID)
	if isNoRows(err) {
		return ValidationOutcome{OK: false, Kind: llm.ErrorKindNotConfigured}, nil
	}
	if err != nil {
		return ValidationOutcome{}, err
	}
	if !rowCredsConfigured(row) {
		return ValidationOutcome{OK: false, Kind: llm.ErrorKindNotConfigured}, nil
	}
	plainKey := ""
	if len(row.ApiKeyEncrypted) > 0 {
		plain, err := r.box.Open(row.ApiKeyEncrypted)
		if err != nil {
			return ValidationOutcome{}, err
		}
		plainKey = string(plain)
	}
	outcome := r.validate(ctx, row.BaseUrl.String, plainKey, row.Model.String)

	nowValidated := pgtype.Timestamptz{Time: r.now(), Valid: true}
	if err := r.store.UpdateSelfEvolutionModelConfigValidation(ctx, db.UpdateSelfEvolutionModelConfigValidationParams{
		WorkspaceID:        workspaceID,
		LastValidatedAt:    nowValidated,
		LastValidationOk:   pgtype.Bool{Bool: outcome.OK, Valid: true},
		LastValidationError: maskedOrEmpty(outcome),
	}); err != nil {
		return outcome, err
	}
	r.invalidate(workspaceID)
	return outcome, nil
}

// RestoreDefault deletes the override row (credentials and validation state
// with it) — 恢复部署默认.
func (r *Resolver) RestoreDefault(ctx context.Context, workspaceID pgtype.UUID) error {
	if err := r.store.DeleteSelfEvolutionModelConfig(ctx, workspaceID); err != nil {
		return err
	}
	r.invalidate(workspaceID)
	return nil
}

// Override exposes the stored row to the GET handler (credential column
// excluded by the caller's mapping). exists=false when no row.
func (r *Resolver) Override(ctx context.Context, workspaceID pgtype.UUID) (db.SelfEvolutionModelConfig, bool, error) {
	row, err := r.store.GetSelfEvolutionModelConfig(ctx, workspaceID)
	if isNoRows(err) {
		return db.SelfEvolutionModelConfig{}, false, nil
	}
	if err != nil {
		return db.SelfEvolutionModelConfig{}, false, err
	}
	return row, true, nil
}

// --- internals ---

// ErrIncompleteCreds is returned when credentials are half-saved: base URL
// and model must be provided together (or both blank for a switch-only save).
var ErrIncompleteCreds = &incompleteCredsError{}

type incompleteCredsError struct{}

func (*incompleteCredsError) Error() string {
	return "selfevconfig: base URL and model must be saved together"
}

func (r *Resolver) buildClient(row db.SelfEvolutionModelConfig) (*llm.Client, error) {
	cfg := llm.Config{
		BaseURL:      row.BaseUrl.String,
		DefaultModel: row.Model.String,
	}
	if len(row.ApiKeyEncrypted) > 0 {
		plain, err := r.box.Open(row.ApiKeyEncrypted)
		if err != nil {
			return nil, err
		}
		cfg.APIKey = string(plain)
	}
	return llm.New(cfg), nil
}

// validate pings the gateway with a minimal completion. Errors are
// classified (llm.Classify) and their text masked before leaving this
// package.
func (r *Resolver) validate(ctx context.Context, baseURL, apiKey, model string) ValidationOutcome {
	if baseURL == "" || model == "" {
		return ValidationOutcome{OK: false, Kind: llm.ErrorKindNotConfigured, Message: "base URL and model are both required"}
	}
	client := llm.New(llm.Config{APIKey: apiKey, BaseURL: baseURL, DefaultModel: model})
	pingCtx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()
	_, err := client.GenerateText(pingCtx, model, "You are a connectivity health check. Reply with the single word: pong.", "ping")
	if err != nil {
		// The summary names the classified category plus at most the HTTP
		// status. The raw SDK error (full response body, endpoint URL) must
		// not reach the config row or any response.
		msg := maskSecrets(llm.Summary(err), apiKey)
		if len(msg) > maxValidationMessage {
			msg = msg[:maxValidationMessage]
		}
		return ValidationOutcome{OK: false, Kind: llm.Classify(err), Message: msg}
	}
	return ValidationOutcome{OK: true, Kind: llm.ErrorKindNone, ValidatedAt: r.now()}
}

func maskedOrEmpty(outcome ValidationOutcome) string {
	if outcome.OK {
		return ""
	}
	return outcome.Message
}

// plainKeyIf returns the plaintext key when the sealed form is non-empty —
// validate() takes plaintext while Save already sealed; this keeps one call
// site each way.
func plainKeyIf(sealed []byte, r *Resolver) string {
	if len(sealed) == 0 {
		return ""
	}
	plain, err := r.box.Open(sealed)
	if err != nil {
		return ""
	}
	return string(plain)
}

// maskSecrets replaces any occurrence of the secrets with a fixed mask so a
// hostile gateway echoing the Authorization header cannot launder the key
// into the config row or a response.
func maskSecrets(msg string, secrets ...string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "••••")
	}
	return msg
}

func rowCredsConfigured(row db.SelfEvolutionModelConfig) bool {
	return row.BaseUrl.Valid && strings.TrimSpace(row.BaseUrl.String) != "" &&
		row.Model.Valid && strings.TrimSpace(row.Model.String) != ""
}

func isNoRows(err error) bool { return err != nil && err == pgx.ErrNoRows }

func (r *Resolver) cached(ws pgtype.UUID) (RunTarget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[ws]
	if !ok || r.now().After(e.expires) {
		return RunTarget{}, false
	}
	return e.target, true
}

func (r *Resolver) storeCache(ws pgtype.UUID, target RunTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[ws] = cacheEntry{target: target, expires: r.now().Add(cacheTTL)}
}

func (r *Resolver) invalidate(ws pgtype.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, ws)
}
