package main

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRepairOAuthMigrationLedger(t *testing.T) {
	sql, err := os.ReadFile("repair_oauth_migration_ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	oldVersions := []string{"929_oauth_client", "930_oauth_client_client_id_index"}
	newVersions := []string{"939_oauth_client", "940_oauth_client_client_id_index"}
	for _, tc := range []struct {
		name    string
		initial []string
		want    []string
		remove  string
	}{
		{"fresh", nil, nil, ""},
		{"old", oldVersions, newVersions, ""},
		{"new", newVersions, newVersions, ""},
		{"both", append(append([]string{}, oldVersions...), newVersions...), newVersions, ""},
		{"mixed", []string{oldVersions[0], newVersions[1]}, newVersions, ""},
		{"missing-table", oldVersions, oldVersions, "DROP TABLE oauth_client"},
		{"missing-index", oldVersions, oldVersions, "DROP INDEX idx_oauth_client_client_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			conn, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.Exec(ctx, "RESET search_path"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := conn.Exec(ctx, `
				CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL);
				CREATE TABLE oauth_client (client_id TEXT NOT NULL);
				CREATE UNIQUE INDEX idx_oauth_client_client_id ON oauth_client (client_id);
				INSERT INTO oauth_client VALUES ('ledger-test-client');
				INSERT INTO schema_migrations VALUES ('untouched', '2026-01-01T00:00:00Z');
			`); err != nil {
				t.Fatal(err)
			}
			for _, version := range tc.initial {
				if _, err := conn.Exec(ctx, "INSERT INTO schema_migrations VALUES ($1, '2026-01-01T00:00:00Z')", version); err != nil {
					t.Fatal(err)
				}
			}
			if tc.remove != "" {
				if _, err := conn.Exec(ctx, tc.remove); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Exec(ctx, string(sql)); err == nil {
					t.Fatal("schema drift must reject ledger repair")
				}
				if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
					t.Fatal(err)
				}
				want := append(append([]string{}, tc.want...), "untouched")
				if got := f.appliedVersions(t); !reflect.DeepEqual(got, want) {
					t.Fatalf("rejected repair changed versions: got %v, want %v", got, want)
				}
				return
			}
			for range 2 {
				if _, err := conn.Exec(ctx, string(sql)); err != nil {
					t.Fatal(err)
				}
			}
			want := append(append([]string{}, tc.want...), "untouched")
			if got := f.appliedVersions(t); !reflect.DeepEqual(got, want) {
				t.Fatalf("versions = %v, want %v", got, want)
			}
			var unchanged bool
			if err := conn.QueryRow(ctx, `SELECT
				(SELECT count(*) = 1 FROM oauth_client WHERE client_id = 'ledger-test-client')
				AND NOT EXISTS (SELECT 1 FROM schema_migrations WHERE applied_at <> '2026-01-01T00:00:00Z')
			`).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("client and timestamps preserved = %v, error = %v", unchanged, err)
			}
		})
	}
}
