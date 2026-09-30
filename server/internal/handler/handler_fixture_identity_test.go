package handler

import (
	"context"
	"testing"
)

// Two test processes sharing one database used to race on one fixed fixture
// identity (RUYI-311): process B's setup cleanup deleted process A's live
// fixture user and workspace, and A's later tests failed on missing rows and
// FK violations that looked like product regressions. The fixture identity
// must therefore be process-unique, and one identity's cleanup must leave
// another identity's rows alone.
func TestHandlerFixtureIdentitiesAreProcessUnique(t *testing.T) {
	ctx := context.Background()

	emailA, slugA := makeHandlerFixtureIdentity()
	emailB, slugB := makeHandlerFixtureIdentity()

	if emailA == emailB || slugA == slugB {
		t.Fatalf("two fixture identities collided: email %q slug %q", emailA, emailB)
	}

	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, handlerTestName, emailA).Scan(&userID); err != nil {
		t.Fatalf("seed identity A user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = $1`, emailA)
	})
	var wsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description) VALUES ($1, $2, '') RETURNING id
	`, "Fixture Identity A", slugA).Scan(&wsID); err != nil {
		t.Fatalf("seed identity A workspace: %v", err)
	}

	// Identity B's cleanup runs "in the other process": it must not touch A.
	if err := cleanupHandlerFixtureIdentity(ctx, testPool, emailB, slugB); err != nil {
		t.Fatalf("cleanup identity B: %v", err)
	}
	var alive int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace WHERE id = $1`, wsID).Scan(&alive); err != nil {
		t.Fatalf("count identity A workspace: %v", err)
	}
	if alive != 1 {
		t.Fatalf("identity B cleanup deleted identity A's workspace: mutual deletion is back")
	}

	if err := cleanupHandlerFixtureIdentity(ctx, testPool, emailA, slugA); err != nil {
		t.Fatalf("cleanup identity A: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace WHERE id = $1`, wsID).Scan(&alive); err != nil {
		t.Fatalf("count identity A workspace after own cleanup: %v", err)
	}
	if alive != 0 {
		t.Fatalf("identity A cleanup left its workspace behind")
	}
}

// Process-unique fixture names mean a run that dies before cleanup leaks its
// rows: the next run no longer reuses (and so no longer removes) the same
// fixed names. The stale sweep is what keeps that bounded — everything in the
// handler fixture namespace older than any possible test process is residue
// and must go, while anything fresh stays.
func TestStaleHandlerFixtureSweep(t *testing.T) {
	ctx := context.Background()

	_, staleSlug := makeHandlerFixtureIdentity()
	staleSlug = "handler-tests-" + staleSlug + "-stale"
	_, freshSlug := makeHandlerFixtureIdentity()
	freshSlug = "handler-tests-" + freshSlug + "-fresh"

	insertAgedWorkspace := func(slug string, age string) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workspace (name, slug, description, created_at)
			VALUES ($1, $2, '', now() - $3::interval) RETURNING id
		`, "Sweep "+slug, slug, age).Scan(&id); err != nil {
			t.Fatalf("seed sweep workspace %s: %v", slug, err)
		}
		return id
	}

	staleWS := insertAgedWorkspace(staleSlug, "25 hours")
	freshWS := insertAgedWorkspace(freshSlug, "0 seconds")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id IN ($1, $2)`, staleWS, freshWS)
	})

	if err := sweepStaleHandlerFixtures(ctx, testPool); err != nil {
		t.Fatalf("sweep stale fixtures: %v", err)
	}

	var alive int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace WHERE id = $1`, staleWS).Scan(&alive); err != nil {
		t.Fatalf("count stale workspace: %v", err)
	}
	if alive != 0 {
		t.Fatalf("stale fixture workspace survived the sweep")
	}
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace WHERE id = $1`, freshWS).Scan(&alive); err != nil {
		t.Fatalf("count fresh workspace: %v", err)
	}
	if alive != 1 {
		t.Fatalf("sweep deleted a fresh fixture workspace")
	}
}
