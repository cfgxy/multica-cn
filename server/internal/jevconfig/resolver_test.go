package jevconfig

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeStore struct {
	mu   sync.Mutex
	rows map[pgtype.UUID]db.SelfEvolutionJevConfig
}

func (s *fakeStore) GetSelfEvolutionJevConfig(_ context.Context, ws pgtype.UUID) (db.SelfEvolutionJevConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[ws]
	if !ok {
		return db.SelfEvolutionJevConfig{}, pgx.ErrNoRows
	}
	return row, nil
}

func (s *fakeStore) UpsertSelfEvolutionJevConfig(_ context.Context, p db.UpsertSelfEvolutionJevConfigParams) (db.SelfEvolutionJevConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := db.SelfEvolutionJevConfig{
		WorkspaceID: p.WorkspaceID, BaseUrl: p.BaseUrl, ApiKeyEncrypted: p.ApiKeyEncrypted,
		Model: p.Model, TimeoutMs: p.TimeoutMs, LastCheckedAt: p.LastCheckedAt,
		LastCheckOk: p.LastCheckOk, LastCheckError: p.LastCheckError, LastEngine: p.LastEngine,
	}
	s.rows[p.WorkspaceID] = row
	return row, nil
}

func (s *fakeStore) UpdateSelfEvolutionJevConfigCheck(_ context.Context, p db.UpdateSelfEvolutionJevConfigCheckParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[p.WorkspaceID]
	row.LastCheckedAt = p.LastCheckedAt
	row.LastCheckOk = p.LastCheckOk
	row.LastCheckError = p.LastCheckError
	row.LastEngine = p.LastEngine
	s.rows[p.WorkspaceID] = row
	return nil
}

func (s *fakeStore) DeleteSelfEvolutionJevConfig(_ context.Context, ws pgtype.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, ws)
	return nil
}

func testWorkspace(n byte) pgtype.UUID {
	var ws pgtype.UUID
	for i := range ws.Bytes {
		ws.Bytes[i] = n + byte(i)
	}
	ws.Valid = true
	return ws
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	return box
}

func TestStateReportsUnconfigured(t *testing.T) {
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionJevConfig{}}
	state, err := NewResolver(store, testBox(t)).State(context.Background(), testWorkspace(1))
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusUnconfigured || state.TimeoutMS != defaultTimeoutMS {
		t.Fatalf("state = %#v, want unconfigured with default timeout", state)
	}
}

func TestSaveRecordsAvailableProbeAndEncryptsKey(t *testing.T) {
	var auth string
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"engine": "jev-2026.11", "p_failure": 0.2, "threshold": 0.5, "warn": false, "decision": "pass",
		})
	}))
	defer server.Close()

	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionJevConfig{}}
	box := testBox(t)
	resolver := NewResolver(store, box)
	ws := testWorkspace(2)
	state, err := resolver.Save(context.Background(), ws, SaveInput{
		BaseURL: server.URL, APIKey: "workspace-secret", Model: "jev-local", TimeoutMS: 1500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusAvailable || state.LastEngine != "jev-2026.11" {
		t.Fatalf("state = %#v, want available JEV state", state)
	}
	if auth != "Bearer workspace-secret" {
		t.Fatalf("authorization = %q", auth)
	}
	if payload["input"] != probeInput || len(payload) != 1 {
		t.Fatalf("probe payload = %#v, want only input", payload)
	}
	row := store.rows[ws]
	if string(row.ApiKeyEncrypted) == "workspace-secret" || len(row.ApiKeyEncrypted) == 0 {
		t.Fatal("API key was not encrypted at rest")
	}
	client, err := resolver.Client(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if client.Model != "jev-local" || client.APIKey != "workspace-secret" {
		t.Fatalf("client = %#v, want decrypted configured client", client)
	}
}

func TestSaveOfflinePreservesExistingCredential(t *testing.T) {
	store := &fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionJevConfig{}}
	box := testBox(t)
	resolver := NewResolver(store, box)
	ws := testWorkspace(3)
	if _, err := resolver.Save(context.Background(), ws, SaveInput{
		BaseURL: "http://127.0.0.1:1", APIKey: "original-secret", Model: "jev-local", TimeoutMS: 100,
	}); err != nil {
		t.Fatal(err)
	}
	initial := append([]byte(nil), store.rows[ws].ApiKeyEncrypted...)
	state, err := resolver.Save(context.Background(), ws, SaveInput{
		BaseURL: "http://127.0.0.1:1", Model: "jev-local-next", TimeoutMS: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusOffline || state.LastCheckError != "unreachable" {
		t.Fatalf("state = %#v, want offline unreachable state", state)
	}
	if string(store.rows[ws].ApiKeyEncrypted) != string(initial) {
		t.Fatal("empty API key did not preserve existing encrypted credential")
	}
}

func TestSaveRejectsInvalidInput(t *testing.T) {
	resolver := NewResolver(&fakeStore{rows: map[pgtype.UUID]db.SelfEvolutionJevConfig{}}, testBox(t))
	ws := testWorkspace(4)
	cases := []struct {
		name string
		in   SaveInput
		want error
	}{
		{name: "missing model", in: SaveInput{BaseURL: "https://jev.example", TimeoutMS: 1500}, want: ErrIncompleteConfig},
		{name: "credential in URL", in: SaveInput{BaseURL: "https://key@jev.example", Model: "m", TimeoutMS: 1500}, want: ErrInvalidEndpoint},
		{name: "timeout too small", in: SaveInput{BaseURL: "https://jev.example", Model: "m", TimeoutMS: 99}, want: ErrInvalidTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolver.Save(context.Background(), ws, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("Save error = %v, want %v", err, tc.want)
			}
		})
	}
}
