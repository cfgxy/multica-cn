package testutil

import (
	"context"
	"os"
	"testing"
	"time"
)

// ledgerDrift classifies the difference between the branch's on-disk
// migration set and the database's schema_migrations ledger (RUYI-311):
// extras = ledger rows no file in this branch defines (the database was
// migrated by a different branch — a superset schema that turns dynamic
// schema-manifest tests structurally red), missing = branch migrations the
// database has not applied.
func TestLedgerDrift(t *testing.T) {
	branch := []string{"001_init", "029_daemon_token", "969_channel_chat_run_intent_claim_idx"}

	extras, missing := ledgerDrift(branch, []string{
		"001_init",
		"029_daemon_token",
		"969_channel_chat_run_intent_claim_idx",
	})
	if len(extras) != 0 || len(missing) != 0 {
		t.Fatalf("aligned sets must not drift: extras=%v missing=%v", extras, missing)
	}

	extras, missing = ledgerDrift(branch, []string{
		"001_init",
		"029_daemon_token",
		"969_channel_chat_run_intent_claim_idx",
		"973_agent_task_cancel_attribution",
	})
	if len(extras) != 1 || extras[0] != "973_agent_task_cancel_attribution" {
		t.Fatalf("superset ledger must surface the extra version, got extras=%v", extras)
	}
	if len(missing) != 0 {
		t.Fatalf("superset ledger must not report missing versions, got %v", missing)
	}

	extras, missing = ledgerDrift(branch, []string{"001_init"})
	if len(missing) != 2 {
		t.Fatalf("behind ledger must report both unapplied versions, got missing=%v", missing)
	}
	if len(extras) != 0 {
		t.Fatalf("behind ledger must not report extras, got %v", extras)
	}
}

// CheckMigrationLedger must accept a database whose ledger and physical
// schema match this branch. The premise is environment-specific, so the test
// only runs when pointed at a database claimed to be aligned; a wrong claim
// fails here rather than in the middle of a handler run.
func TestCheckMigrationLedgerOnAlignedDatabase(t *testing.T) {
	dsn := os.Getenv("TEST_MIGRATION_GUARD_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_MIGRATION_GUARD_DATABASE_URL not set; point it at a database migrated by this branch")
	}

	pool, err := newGuardPool(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := CheckMigrationLedger(ctx, pool); err != nil {
		t.Fatalf("aligned database rejected: %v", err)
	}
}
