package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestReconcileRuntimeCostBudgetMigrationRenames(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tableIdent, err := quoteQualifiedIdentifier(f.tableFQN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`, tableIdent)); err != nil {
		t.Fatal(err)
	}

	legacyVersions := []string{
		"963_runtime_cost_budget",
		"964_runtime_cost_budget_pkey_index",
		"965_runtime_cost_budget_primary_key",
		"966_runtime_cost_budget_scope_index",
	}

	seed := func(versions ...string) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, fmt.Sprintf("TRUNCATE %s", tableIdent)); err != nil {
			t.Fatal(err)
		}
		for _, version := range versions {
			if _, err := f.pool.Exec(ctx, fmt.Sprintf("INSERT INTO %s (version) VALUES ($1)", tableIdent), version); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("complete legacy ledger is renamed before SQL replay", func(t *testing.T) {
		seed(legacyVersions...)
		conn, err := f.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if err := reconcileRuntimeCostBudgetMigrationRenames(ctx, conn, tableIdent); err != nil {
			t.Fatal(err)
		}
		for _, legacy := range legacyVersions {
			var count int
			if err := f.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE version = $1", tableIdent), legacy).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Errorf("legacy ledger %s remains", legacy)
			}
			target := runtimeCostBudgetMigrationRenames[legacy]
			if err := f.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE version = $1", tableIdent), target).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("target ledger %s count = %d, want 1", target, count)
			}
		}
	})

	for _, tc := range []struct {
		name     string
		versions []string
	}{
		{name: "partial legacy ledger is rejected", versions: legacyVersions[:3]},
		{name: "mixed legacy and target ledger is rejected", versions: append(append([]string{}, legacyVersions...), "1012_runtime_cost_budget")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed(tc.versions...)
			conn, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			err = reconcileRuntimeCostBudgetMigrationRenames(ctx, conn, tableIdent)
			if err == nil || !strings.Contains(err.Error(), "partial or mixed") {
				t.Fatalf("reconcile error = %v, want partial-or-mixed rejection", err)
			}
		})
	}
}
