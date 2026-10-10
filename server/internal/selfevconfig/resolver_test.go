package selfevconfig

// Tests for the per-workspace model-service resolver (RUYI-551). The
// priority chain is the walkthrough's server-logic core: module config
// (this table) > deploy-injected default (MULTICA_LLM_*) > unconfigured.
// Wiring is proven end-to-end against an httptest gateway: when the module
// row wins, the actual model request must hit the row's base URL, carry the
// decrypted key in the Authorization header, and request the row's model —
// never the deploy defaults.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/llm"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// --- fixtures ---

type fakeStore struct {
	mu      sync.Mutex
	rows    map[pgtype.UUID]db.SelfEvolutionModelConfig
	upserts int
	lastUp  db.UpsertSelfEvolutionModelConfigParams
	vals    int
	lastVal db.UpdateSelfEvolutionModelConfigValidationParams
	deletes int
}

func (s *fakeStore) GetSelfEvolutionModelConfig(_ context.Context, ws pgtype.UUID) (db.SelfEvolutionModelConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[ws]
	if !ok {
		return db.SelfEvolutionModelConfig{}, pgx.ErrNoRows
	}
	return row, nil
}

func (s *fakeStore) UpsertSelfEvolutionModelConfig(_ context.Context, p db.UpsertSelfEvolutionModelConfigParams) (db.SelfEvolutionModelConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts++
	s.lastUp = p
	row := db.SelfEvolutionModelConfig{
		WorkspaceID:         p.WorkspaceID,
		BaseUrl:             p.BaseUrl,
		ApiKeyEncrypted:     p.ApiKeyEncrypted,
		Model:               p.Model,
		ScoringEnabled:      p.ScoringEnabled,
		LastValidatedAt:     p.LastValidatedAt,
		LastValidationOk:    p.LastValidationOk,
		LastValidationError: p.LastValidationError,
	}
	s.rows[p.WorkspaceID] = row
	return row, nil
}

func (s *fakeStore) UpdateSelfEvolutionModelConfigValidation(_ context.Context, p db.UpdateSelfEvolutionModelConfigValidationParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals++
	s.lastVal = p
	return nil
}

func (s *fakeStore) DeleteSelfEvolutionModelConfig(_ context.Context, ws pgtype.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.rows, ws)
	return nil
}

func testWS(n int) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = byte(n + i)
	}
	u.Valid = true
	return u
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	return box
}

// gateway is a stand-in OpenAI-compatible server recording what the resolver
// actually sent: the Authorization header, the requested model, and the
// number of calls.
type gateway struct {
	srv    *httptest.Server
	mu     sync.Mutex
	auth   string
	model  string
	calls  int
	status int // response status; 0 = 200
	body   string
}

func newGateway(t *testing.T, status int) *gateway {
	t.Helper()
	g := &gateway{status: status}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.auth = r.Header.Get("Authorization")
		defer g.mu.Unlock()
		g.calls++
		payload, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(payload, &req)
		g.model = req.Model
		if status != 0 {
			// Echo the Authorization header into the error body the way some
			// gateways do — the persisted message must not carry it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			payload, _ := json.Marshal(map[string]any{
				"error": map[string]any{"message": "denied (auth=" + g.auth + ")"},
			})
			_, _ = w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": time.Now().Unix(),
			"model": "whatever",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "pong"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func overrideRow(ws pgtype.UUID, g *gateway, apiKey string, model string) db.SelfEvolutionModelConfig {
	return db.SelfEvolutionModelConfig{
		WorkspaceID: ws,
		BaseUrl:     pgtype.Text{String: g.srv.URL, Valid: true},
		Model:       pgtype.Text{String: model, Valid: true},
		// api key sealed by the caller when needed
		ScoringEnabled: true,
	}
}

// --- resolution priority chain ---

func TestResolveModuleConfigBeatsDeployDefault(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(1)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	sealed, err := box.Seal([]byte("ui-key"))
	if err != nil {
		t.Fatal(err)
	}
	row := overrideRow(ws, g, "", "ui-model")
	row.ApiKeyEncrypted = sealed
	store.rows[ws] = row

	deploy := llm.New(llm.Config{APIKey: "env-key", BaseURL: "https://deploy.example.invalid", DefaultModel: "env-model"})
	r := NewResolver(store, box, deploy, "env-model")

	target, err := r.ResolveRunTarget(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if target.Source != SourceModuleConfig {
		t.Fatalf("source = %q, want %q", target.Source, SourceModuleConfig)
	}
	if target.Model != "ui-model" {
		t.Fatalf("model = %q, want ui-model", target.Model)
	}
	if !target.Client.Enabled() {
		t.Fatal("resolved client must be enabled")
	}
	// Walkthrough #22 branch: the resolved client actually speaks to the UI
	// configured gateway with the UI model — not the deploy defaults.
	if _, err := target.Client.GenerateText(context.Background(), target.Model, "health", "ping"); err != nil {
		t.Fatalf("generate through resolved client: %v", err)
	}
	if g.calls != 1 {
		t.Fatalf("gateway calls = %d, want 1", g.calls)
	}
	if g.model != "ui-model" {
		t.Fatalf("gateway saw model %q, want ui-model", g.model)
	}
	if g.auth != "Bearer ui-key" {
		t.Fatalf("gateway auth = %q, want the decrypted UI key", g.auth)
	}
}

func TestResolveFallsBackToDeployDefault(t *testing.T) {
	ws := testWS(2)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	deploy := llm.New(llm.Config{APIKey: "env-key", BaseURL: "https://deploy.example.invalid", DefaultModel: "env-model"})
	r := NewResolver(store, box, deploy, "env-model")

	target, err := r.ResolveRunTarget(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if target.Source != SourceDeployDefault {
		t.Fatalf("source = %q, want %q", target.Source, SourceDeployDefault)
	}
	if target.Client != deploy {
		t.Fatal("fallback must reuse the deploy client, not build a new one")
	}
	if target.Model != "env-model" {
		t.Fatalf("model = %q, want env-model", target.Model)
	}
}

func TestResolveSwitchOnlyRowFallsThroughToDeploy(t *testing.T) {
	ws := testWS(3)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{
		ws: {WorkspaceID: ws, ScoringEnabled: false}, // switch off, no credentials
	}}
	box := testBox(t)
	deploy := llm.New(llm.Config{APIKey: "env-key", BaseURL: "https://deploy.example.invalid", DefaultModel: "env-model"})
	r := NewResolver(store, box, deploy, "env-model")

	target, err := r.ResolveRunTarget(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if target.Source != SourceDeployDefault {
		t.Fatalf("source = %q, want %q (switch-only row has no credentials)", target.Source, SourceDeployDefault)
	}
	if on, err := r.ScoringEnabled(context.Background(), ws); err != nil || on {
		t.Fatalf("ScoringEnabled = %v/%v, want false/nil", on, err)
	}
}

func TestResolveUnconfiguredWhenNothingExists(t *testing.T) {
	ws := testWS(4)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	disabled := llm.New(llm.Config{})
	r := NewResolver(store, box, disabled, "")

	target, err := r.ResolveRunTarget(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if target.Source != SourceNone || target.Client != nil {
		t.Fatalf("target = %+v, want none/nil", target)
	}
}

// --- cache invalidation ---

func TestResolverInvalidatesOnSaveAndRestore(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(5)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	disabled := llm.New(llm.Config{})
	r := NewResolver(store, box, disabled, "")

	target, _ := r.ResolveRunTarget(context.Background(), ws)
	if target.Source != SourceNone {
		t.Fatalf("initial source = %q, want none", target.Source)
	}

	if _, failure, err := r.Save(context.Background(), ws, SaveInput{
		BaseURL: g.srv.URL, APIKey: "k1", Model: "m1", ScoringEnabled: true,
	}); err != nil || failure != nil {
		t.Fatalf("save: failure=%v err=%v", failure, err)
	}
	target, _ = r.ResolveRunTarget(context.Background(), ws)
	if target.Source != SourceModuleConfig || target.Model != "m1" {
		t.Fatalf("after save source/model = %q/%q, want module_config/m1", target.Source, target.Model)
	}

	if err := r.RestoreDefault(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	target, _ = r.ResolveRunTarget(context.Background(), ws)
	if target.Source != SourceNone {
		t.Fatalf("after restore source = %q, want none", target.Source)
	}
	if store.deletes != 1 {
		t.Fatalf("deletes = %d, want 1", store.deletes)
	}
}

// --- save: mandatory validation ---

func TestSaveRejectsWhenValidationFailsAndPersistsNothing(t *testing.T) {
	g := newGateway(t, http.StatusUnauthorized)
	ws := testWS(6)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	r := NewResolver(store, box, llm.New(llm.Config{}), "")

	row, failure, err := r.Save(context.Background(), ws, SaveInput{
		BaseURL: g.srv.URL, APIKey: "secret-key-42", Model: "m1", ScoringEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure == nil {
		t.Fatal("save must refuse to persist when mandatory validation fails")
	}
	if failure.Kind != llm.ErrorKindCredentials {
		t.Fatalf("failure kind = %q, want credentials", failure.Kind)
	}
	if strings.Contains(failure.Message, "secret-key-42") {
		t.Fatalf("failure message leaks the key: %q", failure.Message)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0 (nothing persisted on failed validation)", store.upserts)
	}
	if row.WorkspaceID.Valid {
		t.Fatal("no row should be returned on failure")
	}
}

func TestSaveValidatesAndSealsKey(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(7)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	r := NewResolver(store, box, llm.New(llm.Config{}), "")

	_, failure, err := r.Save(context.Background(), ws, SaveInput{
		BaseURL: g.srv.URL, APIKey: "ui-key", Model: "m1", ScoringEnabled: true,
	})
	if err != nil || failure != nil {
		t.Fatalf("save: failure=%v err=%v", failure, err)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}
	up := store.lastUp
	if !up.LastValidationOk.Bool || !up.LastValidatedAt.Valid {
		t.Fatalf("validation outcome not persisted as ok: %+v", up)
	}
	// Key is stored as ciphertext, decryptable with the box, never plaintext.
	if string(up.ApiKeyEncrypted) == "ui-key" {
		t.Fatal("api key stored as plaintext")
	}
	plain, err := box.Open(up.ApiKeyEncrypted)
	if err != nil || string(plain) != "ui-key" {
		t.Fatalf("sealed key round-trip failed: %q / %v", plain, err)
	}
	if g.calls != 1 || g.model != "m1" || g.auth != "Bearer ui-key" {
		t.Fatalf("validation ping wired wrong: calls=%d model=%q auth=%q", g.calls, g.model, g.auth)
	}
}

func TestSaveBlankKeyReusesStoredKey(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(8)
	box := testBox(t)
	sealed, _ := box.Seal([]byte("original-key"))
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{
		ws: {WorkspaceID: ws, BaseUrl: pgtype.Text{String: g.srv.URL, Valid: true},
			Model: pgtype.Text{String: "m0", Valid: true}, ApiKeyEncrypted: sealed, ScoringEnabled: true},
	}}
	r := NewResolver(store, box, llm.New(llm.Config{}), "")

	_, failure, err := r.Save(context.Background(), ws, SaveInput{
		BaseURL: g.srv.URL, APIKey: "", Model: "m1", ScoringEnabled: true,
	})
	if err != nil || failure != nil {
		t.Fatalf("save: failure=%v err=%v", failure, err)
	}
	if g.auth != "Bearer original-key" {
		t.Fatalf("validation auth = %q, want the stored key reused", g.auth)
	}
	if plain, err := box.Open(store.lastUp.ApiKeyEncrypted); err != nil || string(plain) != "original-key" {
		t.Fatalf("stored key not carried over: %q / %v", plain, err)
	}
}

func TestSaveSwitchOnlySkipsValidation(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(9)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	box := testBox(t)
	r := NewResolver(store, box, llm.New(llm.Config{}), "")

	if _, failure, err := r.Save(context.Background(), ws, SaveInput{ScoringEnabled: false}); err != nil || failure != nil {
		t.Fatalf("switch-only save: failure=%v err=%v", failure, err)
	}
	if g.calls != 0 {
		t.Fatalf("gateway calls = %d, want 0 (no credentials to validate)", g.calls)
	}
	if store.lastUp.BaseUrl.Valid || store.lastUp.Model.Valid || store.lastUp.ApiKeyEncrypted != nil {
		t.Fatalf("switch-only save stored credentials: %+v", store.lastUp)
	}
}

// --- revalidate / validate-only ---

func TestRevalidateStoredPersistsOutcome(t *testing.T) {
	g := newGateway(t, 0)
	ws := testWS(10)
	box := testBox(t)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	r := NewResolver(store, box, llm.New(llm.Config{}), "")
	if _, failure, err := r.Save(context.Background(), ws, SaveInput{BaseURL: g.srv.URL, APIKey: "k", Model: "m", ScoringEnabled: true}); err != nil || failure != nil {
		t.Fatalf("save: %v / %v", failure, err)
	}

	out, err := r.RevalidateStored(context.Background(), ws)
	if err != nil || !out.OK {
		t.Fatalf("revalidate: out=%+v err=%v", out, err)
	}
	if store.vals != 1 || !store.lastVal.LastValidationOk.Bool {
		t.Fatalf("outcome not persisted: vals=%d last=%+v", store.vals, store.lastVal)
	}

	// Gateway starts failing: revalidate records the failure with a kind.
	g2 := newGateway(t, http.StatusInternalServerError)
	stored := store.rows[ws]
	stored.BaseUrl = pgtype.Text{String: g2.srv.URL, Valid: true}
	store.rows[ws] = stored
	out, err = r.RevalidateStored(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Kind != llm.ErrorKindConnection {
		t.Fatalf("out = %+v, want failure/connection", out)
	}
	if store.lastVal.LastValidationOk.Bool {
		t.Fatal("failure outcome must be persisted as not-ok")
	}
}

// P2-2: the persisted validation message is the classified summary — never
// the upstream response body or the endpoint URL the SDK error embeds.
func TestRevalidateStoredMessageCarriesNoRawUpstreamDetail(t *testing.T) {
	g := newGateway(t, http.StatusUnauthorized)
	ws := testWS(11)
	box := testBox(t)
	sealed, err := box.Seal([]byte("secret-key-42"))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{
		ws: {WorkspaceID: ws, BaseUrl: pgtype.Text{String: g.srv.URL, Valid: true},
			Model: pgtype.Text{String: "m1", Valid: true}, ApiKeyEncrypted: sealed, ScoringEnabled: true},
	}}
	r := NewResolver(store, box, llm.New(llm.Config{}), "")

	out, err := r.RevalidateStored(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Kind != llm.ErrorKindCredentials {
		t.Fatalf("outcome = ok=%v kind=%q, want a credentials failure", out.OK, out.Kind)
	}
	if store.vals != 1 {
		t.Fatalf("validation updates = %d, want 1", store.vals)
	}
	stored := store.lastVal.LastValidationError
	if stored == "" {
		t.Fatal("failed validation must persist a non-empty message")
	}
	for _, forbidden := range []string{"secret-key-42", "denied", "127.0.0.1", "/chat/completions"} {
		if strings.Contains(stored, forbidden) {
			t.Fatalf("stored message carries raw upstream detail %q: %q", forbidden, stored)
		}
	}
	if !strings.Contains(stored, "HTTP 401") {
		t.Fatalf("stored message should name the classified status, got %q", stored)
	}
	if !store.lastVal.LastValidationOk.Valid || store.lastVal.LastValidationOk.Bool {
		t.Fatalf("failed outcome not persisted as last_validation_ok=false: %+v", store.lastVal.LastValidationOk)
	}
}

func TestRevalidateStoredWithoutRowIsNotAnError(t *testing.T) {
	ws := testWS(11)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	r := NewResolver(store, testBox(t), llm.New(llm.Config{}), "")
	out, err := r.RevalidateStored(context.Background(), ws)
	if err != nil || out.OK {
		t.Fatalf("out=%+v err=%v, want not-ok without error", out, err)
	}
	if store.vals != 0 {
		t.Fatalf("validation writes = %d, want 0", store.vals)
	}
}

// --- scoring state derivation (four states) ---

func TestScoringStateFourStates(t *testing.T) {
	ctx := context.Background()
	box := testBox(t)

	// 1) unconfigured: no row, no deploy client.
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	r := NewResolver(store, box, llm.New(llm.Config{}), "")
	st, err := r.ScoringState(ctx, testWS(20))
	if err != nil || st.Status != StatusUnconfigured {
		t.Fatalf("state = %+v err=%v, want unconfigured", st, err)
	}

	// 2) ok via deploy default: no row, deploy enabled.
	r = NewResolver(store, box, llm.New(llm.Config{APIKey: "env", BaseURL: "https://d.invalid", DefaultModel: "env-m"}), "env-m")
	st, err = r.ScoringState(ctx, testWS(21))
	if err != nil || st.Status != StatusOK || st.EffectiveSource != SourceDeployDefault || st.Model != "env-m" {
		t.Fatalf("state = %+v err=%v, want ok/deploy_default/env-m", st, err)
	}

	// 3) ok via module config with validation data.
	g := newGateway(t, 0)
	ws := testWS(22)
	store2 := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	r = NewResolver(store2, box, llm.New(llm.Config{APIKey: "env", BaseURL: "https://d.invalid", DefaultModel: "env-m"}), "env-m")
	if _, failure, err := r.Save(ctx, ws, SaveInput{BaseURL: g.srv.URL, APIKey: "k", Model: "ui-m", ScoringEnabled: true}); err != nil || failure != nil {
		t.Fatalf("save: %v / %v", failure, err)
	}
	st, err = r.ScoringState(ctx, ws)
	if err != nil || st.Status != StatusOK || st.EffectiveSource != SourceModuleConfig || st.Model != "ui-m" {
		t.Fatalf("state = %+v err=%v, want ok/module_config/ui-m", st, err)
	}
	if !st.LastValidatedAt.Valid || !st.LastValidationOK.Bool {
		t.Fatalf("validation metadata missing: %+v", st)
	}

	// 4) error via module config: flip the stored outcome.
	failed := store2.rows[ws]
	failed.LastValidationOk = pgtype.Bool{Bool: false, Valid: true}
	failed.LastValidationError = "denied"
	store2.rows[ws] = failed
	st, err = r.ScoringState(ctx, ws)
	if err != nil || st.Status != StatusError || st.EffectiveSource != SourceModuleConfig || st.ValidationError != "denied" {
		t.Fatalf("state = %+v err=%v, want error/module_config/denied", st, err)
	}

	// 5) disabled: the switch wins over everything else.
	disabled := store2.rows[ws]
	disabled.ScoringEnabled = false
	store2.rows[ws] = disabled
	st, err = r.ScoringState(ctx, ws)
	if err != nil || st.Status != StatusDisabled {
		t.Fatalf("state = %+v err=%v, want disabled", st, err)
	}
}

// --- misc ---

func TestValidateOnlyNeverTouchesStore(t *testing.T) {
	g := newGateway(t, 0)
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionModelConfig{}}
	r := NewResolver(store, testBox(t), llm.New(llm.Config{}), "")
	out, err := r.ValidateOnly(context.Background(), testWS(30), SaveInput{BaseURL: g.srv.URL, APIKey: "k", Model: "m"})
	if err != nil || !out.OK {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if store.upserts != 0 || store.vals != 0 {
		t.Fatalf("validate-only wrote to the store: upserts=%d vals=%d", store.upserts, store.vals)
	}
}

func TestResolvePropagatesStoreErrors(t *testing.T) {
	ws := testWS(31)
	store := &failingStore{}
	r := NewResolver(store, testBox(t), llm.New(llm.Config{}), "")
	if _, err := r.ResolveRunTarget(context.Background(), ws); err == nil {
		t.Fatal("store failure must propagate")
	}
}

type failingStore struct{ fakeStore }

func (s *failingStore) GetSelfEvolutionModelConfig(_ context.Context, _ pgtype.UUID) (db.SelfEvolutionModelConfig, error) {
	return db.SelfEvolutionModelConfig{}, errors.New("db down")
}
