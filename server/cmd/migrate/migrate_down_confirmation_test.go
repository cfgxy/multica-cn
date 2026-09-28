package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RUYI-236 — migrate down confirmation gate.
//
// Two shared-database incidents (2026-09-26 and 2026-09-27) were caused by
// bare `migrate down` invocations that rolled back hundreds of applied
// migrations in one shot. These tests pin the gate added before any down
// SQL can execute:
//
//  1. The rollback plan (version + .down.sql filename, in execution
//     order) is printed before any confirmation is asked for.
//  2. Without explicit consent — --yes in any environment, or an
//     interactive "y" on a TTY — zero down SQL executes and the ledger
//     is untouched.
//  3. Non-interactive sessions without --yes are refused outright
//     (fail closed); there is no prompt to hang a CI run on.
//  4. A confirmed run keeps the exact pre-gate rollback semantics.
//
// The tests reuse the shared fixture from migrate_concurrent_test.go:
// every run gets its own throwaway schema, its own advisory-lock key and
// a hermetic migrations directory, so neither the real schema_migrations
// table nor any shared schema is ever touched. The down fixtures use bare
// DROP TABLE (no IF EXISTS) on purpose: if a single down statement
// executed on a "refused" path, the surviving-table assertions below
// would fail.
func writeDownMigrations(t *testing.T, f *fixture) []string {
	t.Helper()
	dir := t.TempDir()
	// Reverse order: that is the order migrations.Files("down") produces
	// and the order runMigrations walks, i.e. the true execution order.
	files := make([]string, 0, len(f.tableNames))
	for i := len(f.tableNames) - 1; i >= 0; i-- {
		version := f.versions[i]
		body := fmt.Sprintf(
			"DROP TABLE %s.%s;\n",
			pgxIdentifier(f.schema), pgxIdentifier(f.tableNames[i]),
		)
		path := filepath.Join(dir, version+".down.sql")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write down migration: %v", err)
		}
		files = append(files, path)
	}
	return files
}

func pgxIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func downRunOptions(f *fixture, files []string) runOptions {
	return runOptions{
		Direction:             "down",
		Files:                 files,
		SchemaMigrationsTable: f.tableFQN,
		AdvisoryLockKey:       f.lockKey,
	}
}

func upRunOptions(f *fixture) runOptions {
	return runOptions{
		Direction:             "up",
		Files:                 f.files,
		SchemaMigrationsTable: f.tableFQN,
		AdvisoryLockKey:       f.lockKey,
	}
}

func assertTablesExist(t *testing.T, f *fixture, want bool, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range f.tableNames {
		var regclass *string
		err := f.pool.QueryRow(ctx,
			"SELECT to_regclass($1)::text",
			fmt.Sprintf("%s.%s", f.schema, table),
		).Scan(&regclass)
		if err != nil {
			t.Fatalf("%s: inspect table %s: %v", label, table, err)
		}
		exists := regclass != nil
		if exists != want {
			t.Fatalf("%s: table %s exists=%v, want %v", label, table, exists, want)
		}
	}
}

func countLedgerRows(t *testing.T, f *fixture) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := f.pool.QueryRow(ctx, fmt.Sprintf(
		"SELECT count(*) FROM %s", f.tableFQN,
	)).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

func requireUpApplied(t *testing.T, f *fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runMigrations(ctx, f.pool, upRunOptions(f)); err != nil {
		t.Fatalf("seed up migrations: %v", err)
	}
}

// TestDownGateRefusedWithoutConsent proves the acceptance bar: with no
// --yes and no interactive confirmation, the gate refuses and not a
// single down statement runs — the seeded tables and the full ledger
// must survive untouched, and the plan (version + filename, execution
// order) must still have been printed before refusal.
func TestDownGateRefusedWithoutConsent(t *testing.T) {
	f := newFixture(t)
	requireUpApplied(t, f)
	downFiles := writeDownMigrations(t, f)

	var stdout bytes.Buffer
	proceed, err := gateDownRun(context.Background(), f.pool, downFiles, nil, strings.NewReader(""), false, &stdout, f.tableFQN)
	if err != nil {
		t.Fatalf("gateDownRun: %v", err)
	}
	if proceed {
		t.Fatal("gate allowed down run without any explicit consent")
	}

	// Zero down SQL executed: every seeded table survives, ledger intact.
	assertTablesExist(t, f, true, "refused run")
	if got := countLedgerRows(t, f); got != len(f.versions) {
		t.Fatalf("ledger rows after refusal = %d, want %d", got, len(f.versions))
	}

	// The plan was still printed before the refusal, in execution order
	// (reverse of apply order), with version AND down filename per entry.
	out := stdout.String()
	wantOrder := []string{f.versions[len(f.versions)-1], f.versions[0]}
	first := strings.Index(out, wantOrder[0])
	second := strings.Index(out, wantOrder[1])
	if first < 0 || second < 0 {
		t.Fatalf("plan missing versions:\nwant %q and %q in:\n%s", wantOrder[0], wantOrder[1], out)
	}
	if first > second {
		t.Fatalf("plan not in execution order: %q must precede %q\n%s", wantOrder[0], wantOrder[1], out)
	}
	for i, version := range f.versions {
		if !strings.Contains(out, version+".down.sql") {
			t.Fatalf("plan entry %d missing down filename for %q in:\n%s", i, version, out)
		}
	}
	if !strings.Contains(out, "--yes") {
		t.Fatalf("refusal must name the non-interactive escape hatch (--yes), got:\n%s", out)
	}
}

// TestDownGateConfirmedWithYesFlag proves the confirmed path keeps the
// pre-gate semantics: --yes alone (no TTY) authorizes the run, and the
// subsequent runMigrations(down) rolls every seeded migration back.
func TestDownGateConfirmedWithYesFlag(t *testing.T) {
	f := newFixture(t)
	requireUpApplied(t, f)
	downFiles := writeDownMigrations(t, f)

	var stdout bytes.Buffer
	proceed, err := gateDownRun(context.Background(), f.pool, downFiles, []string{"--yes"}, strings.NewReader(""), false, &stdout, f.tableFQN)
	if err != nil {
		t.Fatalf("gateDownRun(--yes): %v", err)
	}
	if !proceed {
		t.Fatal("gate refused a --yes confirmed run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runMigrations(ctx, f.pool, downRunOptions(f, downFiles)); err != nil {
		t.Fatalf("confirmed down run: %v", err)
	}
	assertTablesExist(t, f, false, "confirmed run")
	if got := countLedgerRows(t, f); got != 0 {
		t.Fatalf("ledger rows after confirmed rollback = %d, want 0", got)
	}
}

// TestDownGateInteractiveConsent covers the TTY paths: an explicit "y"
// authorizes the rollback; anything else — "n", a bare Enter, another
// word, or EOF — refuses it.
func TestDownGateInteractiveConsent(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{name: "y confirms", answer: "y\n", want: true},
		{name: "yes confirms", answer: "YES\n", want: true},
		{name: "n refuses", answer: "n\n", want: false},
		{name: "bare enter refuses", answer: "\n", want: false},
		{name: "word refuses", answer: "yeah sure\n", want: false},
		{name: "eof refuses", answer: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			requireUpApplied(t, f)
			downFiles := writeDownMigrations(t, f)

			var stdout bytes.Buffer
			proceed, err := gateDownRun(context.Background(), f.pool, downFiles, nil, strings.NewReader(tc.answer), true, &stdout, f.tableFQN)
			if err != nil {
				t.Fatalf("gateDownRun(%q): %v", tc.answer, err)
			}
			if proceed != tc.want {
				t.Fatalf("answer %q: proceed=%v, want %v", tc.answer, proceed, tc.want)
			}
			assertTablesExist(t, f, true, "gate decision only")
			if got := countLedgerRows(t, f); got != len(f.versions) {
				t.Fatalf("ledger rows = %d, want %d — gate must not roll back by itself", got, len(f.versions))
			}
		})
	}
}

// TestDownGateNothingToRollBack pins the no-op path: with an empty
// schema_migrations the gate proceeds without demanding confirmation,
// because runMigrations will skip every file — identical to the pre-gate
// behavior for a fresh database.
func TestDownGateNothingToRollBack(t *testing.T) {
	f := newFixture(t)
	downFiles := writeDownMigrations(t, f)

	var stdout bytes.Buffer
	proceed, err := gateDownRun(context.Background(), f.pool, downFiles, nil, strings.NewReader(""), false, &stdout, f.tableFQN)
	if err != nil {
		t.Fatalf("gateDownRun: %v", err)
	}
	if !proceed {
		t.Fatal("gate refused a run with nothing applied; it must stay a no-op")
	}
	if out := stdout.String(); !strings.Contains(out, "no applied migrations") {
		t.Fatalf("empty plan must say nothing is applied, got:\n%s", out)
	}
}

func TestParseDownFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantYes bool
		wantErr bool
	}{
		{name: "no args", args: nil},
		{name: "yes flag", args: []string{"--yes"}, wantYes: true},
		{name: "repeated yes flag", args: []string{"--yes", "--yes"}, wantYes: true},
		{name: "unknown flag fails closed", args: []string{"--ture"}, wantErr: true},
		{name: "short flag fails closed", args: []string{"-y"}, wantErr: true},
		{name: "positional arg fails closed", args: []string{"2"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yes, err := parseDownFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDownFlags(%v) = nil error, want failure", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDownFlags(%v): %v", tc.args, err)
			}
			if yes != tc.wantYes {
				t.Fatalf("parseDownFlags(%v) yes=%v, want %v", tc.args, yes, tc.wantYes)
			}
		})
	}
}

func TestPlanDownRollbacksKeepsExecutionOrderAndFilters(t *testing.T) {
	files := []string{"938_two.down.sql", "937_one.down.sql", "100_never.down.sql"}
	applied := map[string]bool{"938_two": true, "100_never": true}

	entries := planDownRollbacks(files, applied)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (%v)", len(entries), entries)
	}
	if entries[0].Version != "938_two" || entries[1].Version != "100_never" {
		t.Fatalf("execution order broken: %+v", entries)
	}
	if entries[0].Filename != "938_two.down.sql" {
		t.Fatalf("filename = %q, want 938_two.down.sql", entries[0].Filename)
	}
	if planDownRollbacks(files, nil) != nil && len(planDownRollbacks(files, nil)) != 0 {
		t.Fatal("nothing applied must plan zero rollbacks")
	}
}

func TestPrintDownPlanListsVersionAndFilename(t *testing.T) {
	var out bytes.Buffer
	printDownPlan(&out, []rollbackEntry{
		{Version: "938_two", Filename: "938_two.down.sql"},
		{Version: "937_one", Filename: "937_one.down.sql"},
	})
	got := out.String()
	for _, want := range []string{"938_two", "938_two.down.sql", "937_one", "937_one.down.sql", "2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan output missing %q in:\n%s", want, got)
		}
	}
}
