package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRuntimeProfileDeerflowZcodeMigrationRoundTrip exercises migration 907 in
// both directions inside a private schema: up must widen the protocol_family
// whitelist to the two new independent families, down must restore the
// pre-907 whitelist, and neither direction may invalidate a compatibility row
// that still declares 'kimi' while launching one of the bridges — that is the
// shape every existing profile has, and losing it would break them.
func TestRuntimeProfileDeerflowZcodeMigrationRoundTrip(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_runtime_profile_families_" + suffix
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	pool := openTestPoolWithSearchPath(t, schema)

	// Minimal stand-in for the production table: the migration only touches
	// the protocol_family CHECK, and the constraint carries the same name it
	// does in production so DROP CONSTRAINT IF EXISTS finds it.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE runtime_profile (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			display_name TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			command_name TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT runtime_profile_protocol_family_check
				CHECK (protocol_family IN ('kimi', 'claude', 'codex'))
		)
	`); err != nil {
		t.Fatalf("create runtime_profile fixture: %v", err)
	}

	// The shapes a 'kimi' profile can have on an upgrading deployment: the
	// shims the split has to rewrite (bare command, per-machine absolute path
	// override, and the Windows forms of both — the daemon is supported on
	// Windows, where an absolute override uses backslashes and a pip/npm
	// installed bridge is entered through a .exe/.cmd launcher), plus the rows
	// that must be left alone: a genuine Kimi profile and a wrapper whose
	// basename merely starts with a bridge name. Their ids are captured so the
	// rewrite can be proven to be in-place — an agent bound through
	// agent_runtime.profile_id keeps working only if the id survives.
	shimIDs := map[string]string{}
	for _, row := range []struct{ name, command string }{
		{"Legacy ZCode over Kimi", "zcode-acp"},
		{"Legacy DeerFlow over Kimi", "/opt/deerflow/venv/bin/deerflow-acp"},
		{"Legacy ZCode over Kimi (Windows path)", `C:\tools\zcode-acp`},
		{"Legacy ZCode over Kimi (Windows npm shim)", `C:\Users\dev\AppData\Roaming\npm\zcode-acp.cmd`},
		{"Legacy DeerFlow over Kimi (Windows exe)", `C:\Python311\Scripts\deerflow-acp.exe`},
		{"Real Kimi", "kimi"},
		{"Real Kimi (Windows exe)", `C:\tools\kimi.exe`},
		{"Custom wrapper over Kimi", `C:\tools\zcode-acp-wrapper.cmd`},
	} {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO runtime_profile (display_name, protocol_family, command_name)
			VALUES ($1, 'kimi', $2)
			RETURNING id::text
		`, row.name, row.command).Scan(&id); err != nil {
			t.Fatalf("insert %q profile: %v", row.name, err)
		}
		shimIDs[row.name] = id
	}

	const version = "907_runtime_profile_add_deerflow_zcode"
	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	run := func(direction string) error {
		return runMigrations(ctx, pool, runOptions{
			Direction:             direction,
			Files:                 realMigrationFiles(t, []string{version}, direction),
			SchemaMigrationsTable: schema + ".schema_migrations",
			AdvisoryLockKey:       lockKey,
			Hooks:                 hooksForDirection(direction),
		})
	}

	// Before 907 the two families have no place to be stored.
	assertFamilyRejected(t, ctx, pool, "deerflow")
	assertFamilyRejected(t, ctx, pool, "zcode")

	if err := run("up"); err != nil {
		t.Fatalf("apply migration 907: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	for _, family := range []string{"deerflow", "zcode"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO runtime_profile (display_name, protocol_family, command_name)
			VALUES ($1, $2, $3)
		`, "Independent "+family, family, family+"-acp"); err != nil {
			t.Fatalf("after 907 up, protocol_family %q was still rejected: %v", family, err)
		}
	}
	// The pre-existing families must not have been dropped on the way.
	assertFamilyAccepted(t, ctx, pool, "kimi")
	// And something outside the new whitelist must still be refused, so the
	// widening did not degenerate into "anything goes".
	assertFamilyRejected(t, ctx, pool, "deerflow-acp")

	// The shims now carry their real identity, in place. A profile still
	// declaring 'kimi' would keep being registered on kimiBackend by the
	// daemon, which is the defect 907 exists to remove.
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy ZCode over Kimi"], "zcode")
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy DeerFlow over Kimi"], "deerflow")
	// The Windows shapes are the same shim: a backslash-separated absolute
	// override and the .cmd/.exe launchers an npm or pip install produces.
	// Matching only '/' left these running on kimiBackend after the upgrade.
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy ZCode over Kimi (Windows path)"], "zcode")
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy ZCode over Kimi (Windows npm shim)"], "zcode")
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy DeerFlow over Kimi (Windows exe)"], "deerflow")
	// A 'kimi' profile that launches kimi is a real Kimi profile, on either
	// platform — the suffix strip must not turn 'kimi.exe' into a bridge.
	assertProfileFamily(t, ctx, pool, shimIDs["Real Kimi"], "kimi")
	assertProfileFamily(t, ctx, pool, shimIDs["Real Kimi (Windows exe)"], "kimi")
	// A command that merely contains a bridge name is not that bridge: the
	// match is on the whole basename, not a prefix.
	assertProfileFamily(t, ctx, pool, shimIDs["Custom wrapper over Kimi"], "kimi")

	// A profile created after 907 in a new family with a non-standard command
	// has no basename the re-apply could decide from; it is what the down
	// migration's identity backup exists for.
	var customWrapperID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Independent ZCode wrapper', 'zcode', 'company-zcode-wrapper')
		RETURNING id::text
	`).Scan(&customWrapperID); err != nil {
		t.Fatalf("insert custom-command zcode profile: %v", err)
	}

	if err := run("down"); err != nil {
		t.Fatalf("roll back migration 907: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, false)

	// Nothing is left in a family the restored whitelist cannot express: a
	// pre-907 daemon refuses to register a profile whose family it cannot map
	// to a backend, and a pre-907 API cannot edit it back.
	var stranded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_profile WHERE protocol_family IN ('deerflow', 'zcode')`,
	).Scan(&stranded); err != nil {
		t.Fatalf("count rows after rollback: %v", err)
	}
	if stranded != 0 {
		t.Errorf("rows left in the new families after rollback = %d, want 0 — an old daemon cannot interpret them", stranded)
	}
	assertFamilyRejected(t, ctx, pool, "deerflow")
	assertFamilyRejected(t, ctx, pool, "zcode")
	assertFamilyAccepted(t, ctx, pool, "kimi")

	// The round trip returns the shims to the exact state 907 found them in,
	// same ids: the bindings that point at these profiles survive both
	// directions.
	for name := range shimIDs {
		assertProfileFamily(t, ctx, pool, shimIDs[name], "kimi")
	}

	// The profiles created directly in the new families while 907 was applied
	// are folded into the shim shape rather than orphaned.
	var folded int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM runtime_profile
		WHERE protocol_family = 'kimi' AND display_name LIKE 'Independent %'
	`).Scan(&folded); err != nil {
		t.Fatalf("count folded profiles: %v", err)
	}
	if folded != 3 {
		t.Errorf("post-907 profiles folded back onto kimi = %d, want 3", folded)
	}

	// Re-applying after a rollback must restore every identity, including the
	// ones no command basename can decide. Without the down migration's
	// identity backup a custom-command profile stops on 'kimi' permanently,
	// and the daemon then runs it on kimiBackend.
	if err := run("up"); err != nil {
		t.Fatalf("re-apply migration 907: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	assertProfileFamily(t, ctx, pool, shimIDs["Legacy ZCode over Kimi"], "zcode")
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy ZCode over Kimi (Windows path)"], "zcode")
	assertProfileFamily(t, ctx, pool, shimIDs["Legacy DeerFlow over Kimi (Windows exe)"], "deerflow")
	assertProfileFamily(t, ctx, pool, shimIDs["Real Kimi"], "kimi")
	assertProfileFamily(t, ctx, pool, shimIDs["Custom wrapper over Kimi"], "kimi")
	assertProfileFamily(t, ctx, pool, customWrapperID, "zcode")

	// The backup is consumed, so a later deliberate family change is not
	// silently reverted by a subsequent re-run of the same migration.
	var backupExists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('runtime_profile_family_907_backup') IS NOT NULL`,
	).Scan(&backupExists); err != nil {
		t.Fatalf("check identity backup table: %v", err)
	}
	if backupExists {
		t.Error("identity backup table survived the re-apply; a later family change would be reverted by re-running 907")
	}
}

// assertProfileFamily reads one profile back by id. Checking the id (rather
// than counting rows by command_name) is what proves the migration rewrote the
// row in place instead of recreating it: agent_runtime.profile_id and every
// agent bound through it only survive if the id does.
func assertProfileFamily(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx,
		`SELECT protocol_family FROM runtime_profile WHERE id = $1::uuid`, id,
	).Scan(&got); err != nil {
		t.Fatalf("read profile %s: %v", id, err)
	}
	if got != want {
		t.Errorf("profile %s protocol_family = %q, want %q", id, got, want)
	}
}

// assertFamilyAccepted inserts and removes a probe row, so it reports what the
// constraint permits without leaving state behind for the next assertion.
func assertFamilyAccepted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, family string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Probe', $1, 'probe')
	`, family); err != nil {
		t.Fatalf("protocol_family %q should be accepted: %v", family, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM runtime_profile WHERE display_name = 'Probe'`); err != nil {
		t.Fatalf("clean up probe row: %v", err)
	}
}

func assertFamilyRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, family string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Probe', $1, 'probe')
	`, family)
	if err == nil {
		pool.Exec(ctx, `DELETE FROM runtime_profile WHERE display_name = 'Probe'`)
		t.Fatalf("protocol_family %q was stored, want the CHECK to reject it", family)
	}
	if !strings.Contains(err.Error(), "runtime_profile_protocol_family_check") {
		t.Fatalf("protocol_family %q: expected the CHECK to reject the insert, got: %v", family, err)
	}
}

func assertMigrationLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
	).Scan(&exists); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if exists != want {
		t.Errorf("schema_migrations has %q = %v, want %v", version, exists, want)
	}
}
