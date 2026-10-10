package main

// RUYI-666: --expected-revision wiring for the instruction-writing update
// commands (workspace update, squad update), plus the guarded-write outcome
// contract: a write that carried expected_revision and failed with a 5xx or
// at the transport layer may have landed server-side, so the CLI labels it
// AMBIGUOUS and never retries; a 4xx is a deterministic rejection (the write
// did not land). Requests without --expected-revision keep the legacy
// behavior and are never labeled.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// resetUpdateFlagState clears Changed on the shared cobra commands' update
// flags: workspaceCmd/squadCmd are package-level singletons, so a flag Set in
// one test leaks into every later test in the process.
func resetUpdateFlagState(t *testing.T, cmd *cobra.Command, flags ...string) {
	t.Helper()
	for _, name := range flags {
		if f := cmd.Flags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
}

var workspaceUpdateFlagNames = []string{
	"name", "description", "description-stdin", "context", "context-stdin",
	"issue-prefix", "expected-revision",
}

var squadUpdateFlagNames = []string{
	"name", "description", "instructions", "leader", "avatar-url", "expected-revision",
}

func captureStderrForGuardedWrite(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := run()

	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out), err
}

func TestWorkspaceUpdateSendsExpectedRevision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/workspaces" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ws-123","name":"n","slug":"s"}]`))
			return
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/api/workspaces/ws-123" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-123","name":"n","slug":"s","revision":8}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, err := workspaceCmd.Find([]string{"update"})
	if err != nil {
		t.Fatalf("find update command: %v", err)
	}
	resetUpdateFlagState(t, cmd, workspaceUpdateFlagNames...)
	if cmd.Flags().Lookup("expected-revision") == nil {
		t.Fatal("workspace update missing --expected-revision flag")
	}
	_ = cmd.Flags().Set("expected-revision", "7")
	_ = cmd.Flags().Set("context", "new context")

	if err := runWorkspaceUpdate(cmd, []string{"ws-123"}); err != nil {
		t.Fatalf("runWorkspaceUpdate: %v", err)
	}
	if gotBody["expected_revision"] != float64(7) {
		t.Fatalf("expected_revision = %v, want 7 (full body: %#v)", gotBody["expected_revision"], gotBody)
	}
}

func TestSquadUpdateSendsExpectedRevision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	var gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sq-123","name":"n","revision":4}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, err := squadCmd.Find([]string{"update"})
	if err != nil {
		t.Fatalf("find update command: %v", err)
	}
	resetUpdateFlagState(t, cmd, squadUpdateFlagNames...)
	if cmd.Flags().Lookup("expected-revision") == nil {
		t.Fatal("squad update missing --expected-revision flag")
	}
	_ = cmd.Flags().Set("expected-revision", "3")
	_ = cmd.Flags().Set("instructions", "new instructions")

	if err := runSquadUpdate(cmd, []string{"sq-123"}); err != nil {
		t.Fatalf("runSquadUpdate: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %s, want PUT", gotMethod)
	}
	if gotBody["expected_revision"] != float64(3) {
		t.Fatalf("expected_revision = %v, want 3 (full body: %#v)", gotBody["expected_revision"], gotBody)
	}
}

func TestGuardedWorkspaceUpdateMarksAmbiguousOnServerError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/workspaces" {
			// resolveWorkspaceArg reads the workspace list before the PATCH.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ws-123","name":"n","slug":"s"}]`))
			return
		}
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, _ := workspaceCmd.Find([]string{"update"})
	resetUpdateFlagState(t, cmd, workspaceUpdateFlagNames...)
	_ = cmd.Flags().Set("expected-revision", "7")
	_ = cmd.Flags().Set("context", "c")

	stderr, err := captureStderrForGuardedWrite(t, func() error {
		return runWorkspaceUpdate(cmd, []string{"ws-123"})
	})
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
	if !strings.Contains(err.Error(), "PATCH /api/workspaces/ws-123") {
		t.Fatalf("expected the PATCH itself to fail, got: %v", err)
	}
	if !strings.Contains(stderr, "AMBIGUOUS") {
		t.Fatalf("stderr missing AMBIGUOUS marker: %q", stderr)
	}
	if !strings.Contains(stderr, "expected_revision") {
		t.Fatalf("stderr missing guarded-write context: %q", stderr)
	}
}

func TestGuardedWorkspaceUpdateDeterministicOn4xx(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/workspaces" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ws-123","name":"n","slug":"s"}]`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"resource changed since it was loaded","code":"revision_conflict","actual_revision":9}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, _ := workspaceCmd.Find([]string{"update"})

	resetUpdateFlagState(t, cmd, workspaceUpdateFlagNames...)
	_ = cmd.Flags().Set("expected-revision", "7")
	_ = cmd.Flags().Set("context", "c")

	stderr, err := captureStderrForGuardedWrite(t, func() error {
		return runWorkspaceUpdate(cmd, []string{"ws-123"})
	})
	if err == nil {
		t.Fatal("expected error from 409 response")
	}
	if !strings.Contains(err.Error(), "PATCH /api/workspaces/ws-123") {
		t.Fatalf("expected the PATCH itself to fail, got: %v", err)
	}
	if strings.Contains(stderr, "AMBIGUOUS") {
		t.Fatalf("4xx must be deterministic, got AMBIGUOUS marker: %q", stderr)
	}
	if !strings.Contains(err.Error(), "revision_conflict") {
		t.Fatalf("error should surface the structured conflict body: %v", err)
	}
}

func TestGuardedSquadUpdateMarksAmbiguousOnDisconnect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // refuse the connection outright

	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, _ := squadCmd.Find([]string{"update"})

	resetUpdateFlagState(t, cmd, squadUpdateFlagNames...)
	_ = cmd.Flags().Set("expected-revision", "3")
	_ = cmd.Flags().Set("instructions", "i")

	stderr, err := captureStderrForGuardedWrite(t, func() error {
		return runSquadUpdate(cmd, []string{"sq-123"})
	})
	if err == nil {
		t.Fatal("expected transport error from closed server")
	}
	if !strings.Contains(stderr, "AMBIGUOUS") {
		t.Fatalf("stderr missing AMBIGUOUS marker: %q", stderr)
	}
}

func TestUnguardedUpdateNeverMarkedAmbiguous(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/workspaces" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ws-123","name":"n","slug":"s"}]`))
			return
		}
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd, _, _ := workspaceCmd.Find([]string{"update"})

	resetUpdateFlagState(t, cmd, workspaceUpdateFlagNames...)
	_ = cmd.Flags().Set("context", "c") // no --expected-revision

	stderr, err := captureStderrForGuardedWrite(t, func() error {
		return runWorkspaceUpdate(cmd, []string{"ws-123"})
	})
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
	if !strings.Contains(err.Error(), "PATCH /api/workspaces/ws-123") {
		t.Fatalf("expected the PATCH itself to fail, got: %v", err)
	}
	if strings.Contains(stderr, "AMBIGUOUS") {
		t.Fatalf("unguarded write must keep the legacy contract, got AMBIGUOUS marker: %q", stderr)
	}
}
