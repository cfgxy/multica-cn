// Package jevconfig owns the encrypted workspace-scoped configuration for the
// JEV prompt-proposal advisory. It intentionally does not share the model
// service resolver: JEV is a warn-only HTTP sidecar with its own protocol and
// connectivity state.
package jevconfig

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultTimeoutMS = 1500
	minTimeoutMS     = 100
	maxTimeoutMS     = 30000
	probeInput       = "JEV connectivity health check."
)

// Store is the sqlc surface used by Resolver.
type Store interface {
	GetSelfEvolutionJevConfig(context.Context, pgtype.UUID) (db.SelfEvolutionJevConfig, error)
	UpsertSelfEvolutionJevConfig(context.Context, db.UpsertSelfEvolutionJevConfigParams) (db.SelfEvolutionJevConfig, error)
	UpdateSelfEvolutionJevConfigCheck(context.Context, db.UpdateSelfEvolutionJevConfigCheckParams) error
	DeleteSelfEvolutionJevConfig(context.Context, pgtype.UUID) error
}

// Status is the only runtime status the configuration UI exposes.
const (
	StatusUnconfigured = "unconfigured"
	StatusOffline      = "offline"
	StatusAvailable    = "available"
)

const (
	SourceUnconfigured = "unconfigured"
	SourceWorkspace    = "workspace"
	SourceDeployment   = "deployment"
)

// State is the safe presentation form of a saved config. Ciphertext is never
// returned; HasAPIKey is the only fact exposed about credentials.
type State struct {
	Status         string
	Source         string
	BaseURL        string
	Model          string
	TimeoutMS      int32
	HasAPIKey      bool
	LastCheckedAt  pgtype.Timestamptz
	LastCheckOK    pgtype.Bool
	LastCheckError string
	LastEngine     string
}

// SaveInput is the owner-supplied config. APIKey empty retains an already
// stored credential, so an owner can change endpoint timing without retyping
// a secret.
type SaveInput struct {
	BaseURL   string
	APIKey    string
	Model     string
	TimeoutMS int32
}

// Resolver decrypts a stored credential only for an outgoing JEV request.
type Resolver struct {
	store      Store
	box        *secretbox.Box
	deployment *legislation.AdvisoryClient
	now        func() time.Time
}

func NewResolver(store Store, box *secretbox.Box, deployment *legislation.AdvisoryClient) *Resolver {
	return &Resolver{store: store, box: box, deployment: deployment, now: time.Now}
}

// Client returns the workspace's configured client, or the deployment default
// when the workspace has not overridden it.
func (r *Resolver) Client(ctx context.Context, workspaceID pgtype.UUID) (*legislation.AdvisoryClient, error) {
	row, err := r.store.GetSelfEvolutionJevConfig(ctx, workspaceID)
	if isNoRows(err) {
		return r.deploymentClient(), nil
	}
	if err != nil {
		return nil, err
	}
	key, err := r.openKey(row.ApiKeyEncrypted)
	if err != nil {
		return nil, err
	}
	return &legislation.AdvisoryClient{
		BaseURL: row.BaseUrl,
		APIKey:  key,
		Model:   row.Model,
		Timeout: time.Duration(row.TimeoutMs) * time.Millisecond,
	}, nil
}

// State returns a truthful status for the workspace card.
func (r *Resolver) State(ctx context.Context, workspaceID pgtype.UUID) (State, error) {
	row, err := r.store.GetSelfEvolutionJevConfig(ctx, workspaceID)
	if isNoRows(err) {
		return r.deploymentState(ctx), nil
	}
	if err != nil {
		return State{}, err
	}
	status := StatusOffline
	if row.LastCheckOk.Valid && row.LastCheckOk.Bool {
		status = StatusAvailable
	}
	return State{
		Status:         status,
		Source:         SourceWorkspace,
		BaseURL:        row.BaseUrl,
		Model:          row.Model,
		TimeoutMS:      row.TimeoutMs,
		HasAPIKey:      len(row.ApiKeyEncrypted) > 0,
		LastCheckedAt:  row.LastCheckedAt,
		LastCheckOK:    row.LastCheckOk,
		LastCheckError: row.LastCheckError,
		LastEngine:     row.LastEngine,
	}, nil
}

// Save persists the configuration first and then probes it. Persisting a
// failed endpoint is intentional: it is how the owner can see and repair an
// offline configuration without a hidden, rejected save.
func (r *Resolver) Save(ctx context.Context, workspaceID pgtype.UUID, in SaveInput) (State, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.Model = strings.TrimSpace(in.Model)
	if err := validateInput(in); err != nil {
		return State{}, err
	}

	stored, err := r.store.GetSelfEvolutionJevConfig(ctx, workspaceID)
	if err != nil && !isNoRows(err) {
		return State{}, err
	}
	sealed := stored.ApiKeyEncrypted
	if in.APIKey != "" {
		sealed, err = r.box.Seal([]byte(in.APIKey))
		if err != nil {
			return State{}, err
		}
	}
	if _, err := r.store.UpsertSelfEvolutionJevConfig(ctx, db.UpsertSelfEvolutionJevConfigParams{
		WorkspaceID:     workspaceID,
		BaseUrl:         in.BaseURL,
		ApiKeyEncrypted: sealed,
		Model:           in.Model,
		TimeoutMs:       in.TimeoutMS,
		LastCheckError:  "",
		LastEngine:      "",
		LastCheckedAt:   pgtype.Timestamptz{},
		LastCheckOk:     pgtype.Bool{},
	}); err != nil {
		return State{}, err
	}
	if _, err := r.Recheck(ctx, workspaceID); err != nil {
		return State{}, err
	}
	return r.State(ctx, workspaceID)
}

// Recheck records a safe, bounded connectivity result and returns the newest
// state. The advisory call itself never reports an error for a network fault.
func (r *Resolver) Recheck(ctx context.Context, workspaceID pgtype.UUID) (State, error) {
	if _, err := r.store.GetSelfEvolutionJevConfig(ctx, workspaceID); isNoRows(err) {
		return r.deploymentState(ctx), nil
	} else if err != nil {
		return State{}, err
	}
	client, err := r.Client(ctx, workspaceID)
	if err != nil {
		return State{}, err
	}
	if !client.Enabled() {
		return State{Status: StatusUnconfigured, Source: SourceWorkspace, TimeoutMS: defaultTimeoutMS}, nil
	}
	report := client.Assess(ctx, "config_probe", probeInput)
	checkedAt := r.now().UTC()
	if err := r.store.UpdateSelfEvolutionJevConfigCheck(ctx, db.UpdateSelfEvolutionJevConfigCheckParams{
		WorkspaceID:    workspaceID,
		LastCheckedAt:  pgtype.Timestamptz{Time: checkedAt, Valid: true},
		LastCheckOk:    pgtype.Bool{Bool: report.Available, Valid: true},
		LastCheckError: report.SkippedReason,
		LastEngine:     report.Engine,
	}); err != nil {
		return State{}, err
	}
	return r.State(ctx, workspaceID)
}

func (r *Resolver) deploymentClient() *legislation.AdvisoryClient {
	if r.deployment != nil {
		return r.deployment
	}
	return &legislation.AdvisoryClient{}
}

func (r *Resolver) deploymentState(ctx context.Context) State {
	client := r.deploymentClient()
	timeoutMS := int32(client.Timeout / time.Millisecond)
	if timeoutMS <= 0 {
		timeoutMS = defaultTimeoutMS
	}
	state := State{
		Status:    StatusUnconfigured,
		Source:    SourceUnconfigured,
		BaseURL:   client.BaseURL,
		Model:     client.Model,
		TimeoutMS: timeoutMS,
	}
	if !client.Enabled() {
		return state
	}
	report := client.Assess(ctx, "config_probe", probeInput)
	checkedAt := r.now().UTC()
	state.Source = SourceDeployment
	state.LastCheckedAt = pgtype.Timestamptz{Time: checkedAt, Valid: true}
	state.LastCheckOK = pgtype.Bool{Bool: report.Available, Valid: true}
	state.LastCheckError = report.SkippedReason
	state.LastEngine = report.Engine
	if report.Available {
		state.Status = StatusAvailable
	} else {
		state.Status = StatusOffline
	}
	return state
}

func (r *Resolver) Delete(ctx context.Context, workspaceID pgtype.UUID) error {
	return r.store.DeleteSelfEvolutionJevConfig(ctx, workspaceID)
}

func (r *Resolver) openKey(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	plain, err := r.box.Open(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func validateInput(in SaveInput) error {
	if in.BaseURL == "" || in.Model == "" {
		return ErrIncompleteConfig
	}
	parsed, err := url.ParseRequestURI(in.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return ErrInvalidEndpoint
	}
	if in.TimeoutMS < minTimeoutMS || in.TimeoutMS > maxTimeoutMS {
		return ErrInvalidTimeout
	}
	return nil
}

var (
	ErrIncompleteConfig = errors.New("jevconfig: base URL and model are required")
	ErrInvalidEndpoint  = errors.New("jevconfig: endpoint must be an http or https URL without credentials")
	ErrInvalidTimeout   = errors.New("jevconfig: timeout must be between 100 and 30000 milliseconds")
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
