package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRequireHumanActor_AllowsHumanRequest pins the happy path: a
// request that passed Auth as a JWT or mul_ PAT does NOT carry
// X-Actor-Source, so the guard lets it through and the inner handler
// runs.
//
// We construct a bare http.Handler chain (no full router) so the test
// exercises only the middleware logic and is independent of any
// downstream wiring.
func TestRequireHumanActor_AllowsHumanRequest(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireHumanActor(next)

	req := httptest.NewRequest(http.MethodGet, "/api/cloud-billing/balance", nil)
	// No X-Actor-Source — this is the JWT / mul_ PAT shape.
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Fatal("inner handler must run for non-task-token requests")
	}
}

// TestRequireHumanActor_BlocksMachineCredentials walks every machine-
// credential X-Actor-Source value the auth middlewares stamp today
// and confirms each is rejected with 403. The values must stay
// in lockstep with auth.go and daemon_auth.go: a new machine
// credential added there without a corresponding case here would
// silently grant agents/nodes account-level access.
func TestRequireHumanActor_BlocksMachineCredentials(t *testing.T) {
	cases := []struct {
		name        string
		actorSource string
	}{
		// mat_ task token — set in middleware/auth.go's mat_ branch.
		// An agent process holding its task-scoped token must not be
		// able to read its owner's billing data.
		{name: "task_token", actorSource: "task_token"},
		// mcn_ cloud-node PAT — set in BOTH middleware/auth.go and
		// middleware/daemon_auth.go's mcn_ branches. A cloud-runtime
		// EC2 node operating on the owner's behalf is the same kind
		// of machine credential as mat_ for billing-authorization
		// purposes.
		{name: "cloud_pat", actorSource: "cloud_pat"},
		// MCP OAuth access token (RUYI-209) — set in middleware/auth.go's
		// RS256 branch. The token lives in an external MCP client's storage
		// rather than with the human who authorized it once, so account-level
		// routes (PAT management, instance admin) must refuse it.
		{name: "oauth", actorSource: "oauth"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("inner handler must NOT run for actor source %q", tc.actorSource)
			})
			mw := RequireHumanActor(next)

			req := httptest.NewRequest(http.MethodGet, "/api/cloud-billing/balance", nil)
			// This is what the Auth (or DaemonAuth) middleware sets
			// for the matching token kind. Setting it directly here
			// proves the gate triggers on the header regardless of
			// upstream context — the auth middlewares strip any
			// client-supplied value before stamping their own, so a
			// non-empty value at this point IS authoritative.
			req.Header.Set("X-Actor-Source", tc.actorSource)
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
	}
}

// TestRequireHumanActor_IgnoresUnknownActorSource pins the gate's
// scope: it is an explicit denylist against the known-bad
// "task_token" value, NOT an allowlist against "human only / empty".
//
// Why the denylist shape:
//
//   - The Auth middleware today sets X-Actor-Source for exactly one
//     case: mat_ task tokens. Every other authenticated path (JWT,
//     mul_ PAT) leaves the header empty. So "non-empty AND not
//     task_token" is unreachable in current production.
//
//   - If a future actor kind is added (say a hypothetical
//     `service_account` token), this gate's silence on the new value
//     is a CONSCIOUS DECISION POINT, not an accident. The added auth
//     branch is the right place to decide whether the new kind should
//     be allowed at billing endpoints — and that decision belongs in
//     a security review at the time, not in a default-deny rule here
//     that pre-emptively shuts out hypothetical use cases we cannot
//     reason about today.
//
// If you are reading this comment because a new actor kind needs to
// reach billing or needs to be blocked from it, update
// RequireHumanActor to handle the new kind explicitly (and update
// this test's expectation accordingly).
func TestRequireHumanActor_IgnoresUnknownActorSource(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireHumanActor(next)

	// A hypothetical future value the gate doesn't know about.
	req := httptest.NewRequest(http.MethodGet, "/api/cloud-billing/balance", nil)
	req.Header.Set("X-Actor-Source", "future_kind")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — gate should only block exact 'task_token'", w.Code)
	}
	if !called {
		t.Fatal("inner handler must run for unknown actor sources")
	}
}

// TestRequireHumanActor_AppliedViaChiRouterUse pins the wiring side of
// the contract: when the guard is attached to a chi route group via
// r.Use, every endpoint in that group is protected, and a task-token
// request never reaches the handler — even one we add later. This is
// what router.go's r.Route("/api/cloud-billing", ...) + r.Use(...)
// guarantees in production; the test is small but a developer adding
// a new billing endpoint and forgetting to re-attach the middleware
// would not be caught by the per-handler tests above.
func TestRequireHumanActor_AppliedViaChiRouterUse(t *testing.T) {
	// Use a real chi router so we exercise r.Use(), not just the
	// middleware function in isolation.
	r := chi.NewRouter()
	r.Use(RequireHumanActor)
	r.Get("/billing/probe", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner handler must NOT run when guard rejects")
	})

	req := httptest.NewRequest(http.MethodGet, "/billing/probe", nil)
	req.Header.Set("X-Actor-Source", "task_token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// TestRequireHumanOrOAuthActor_AllowsHumanAndOAuth (RUYI-868) pins the
// two actor shapes the legislation surface must admit: a plain human
// request (no X-Actor-Source) and an MCP OAuth access token
// (X-Actor-Source: oauth). The OAuth admission is deliberate — those
// tokens are tiered by oauth.ScopeForRequest on every request, and the
// owner-only decide-and-execute endpoints keep RequireHumanActor, so
// the OAuth client can reach the reads and the member-side proposal
// actions and nothing more.
func TestRequireHumanOrOAuthActor_AllowsHumanAndOAuth(t *testing.T) {
	cases := []struct {
		name        string
		actorSource string
	}{
		{name: "human", actorSource: ""},
		{name: "oauth", actorSource: "oauth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			mw := RequireHumanOrOAuthActor(next)

			req := httptest.NewRequest(http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
			if tc.actorSource != "" {
				// Same trust argument as the RequireHumanActor tests: the
				// auth middlewares strip any client-supplied value before
				// stamping their own, so setting the header directly here
				// proves the gate's decision on the source alone.
				req.Header.Set("X-Actor-Source", tc.actorSource)
			}
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 for actor source %q", w.Code, tc.actorSource)
			}
			if !called {
				t.Fatal("inner handler must run for human and oauth actors")
			}
		})
	}
}

// TestRequireHumanOrOAuthActor_BlocksUnscopedMachineCredentials (RUYI-868)
// pins the two machine-credential shapes the legislation surface must keep
// denying: mat_ task tokens and mcn_ cloud-node PATs. They carry no OAuth
// scope tier, so a compromised agent process or cloud node must not be able
// to read clause text or walk the proposal pool.
func TestRequireHumanOrOAuthActor_BlocksUnscopedMachineCredentials(t *testing.T) {
	for _, actorSource := range []string{"task_token", "cloud_pat"} {
		t.Run(actorSource, func(t *testing.T) {
			next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("inner handler must NOT run for actor source %q", actorSource)
			})
			mw := RequireHumanOrOAuthActor(next)

			req := httptest.NewRequest(http.MethodGet, "/api/prompt-legislation/proposals", nil)
			req.Header.Set("X-Actor-Source", actorSource)
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
	}
}

// TestRequireHumanOrOAuthActor_IgnoresUnknownActorSource (RUYI-868) keeps
// the new guard's denylist discipline in lockstep with
// isMachineCredentialActor's: an actor source nobody has classified yet is
// human-equivalent by default, and classifying a new credential kind is a
// security-review decision point, not a default-deny surprise.
func TestRequireHumanOrOAuthActor_IgnoresUnknownActorSource(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RequireHumanOrOAuthActor(next)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
	req.Header.Set("X-Actor-Source", "future_kind")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — unknown actor sources stay admitted", w.Code)
	}
	if !called {
		t.Fatal("inner handler must run for unknown actor sources")
	}
}

// TestRequireHumanOrOAuthActor_AppliedViaChiRouterUse (RUYI-868) pins the
// router wiring: a task-token request is rejected at the guard and never
// reaches the handler behind it, while an OAuth request passes through the
// same group.
func TestRequireHumanOrOAuthActor_AppliedViaChiRouterUse(t *testing.T) {
	r := chi.NewRouter()
	r.Use(RequireHumanOrOAuthActor)
	r.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Actor-Source", "task_token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("task_token: status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Actor-Source", "oauth")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("oauth: status = %d, want 200", w.Code)
	}
}
