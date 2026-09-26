package migrations

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReconcileLedgerReportsRenumberedEntryWhoseNewStemIsAlreadyApplied(t *testing.T) {
	report := ReconcileLedger(
		[]string{"901_project_instructions"},
		[]string{"441_project_instructions", "901_project_instructions"},
	)

	want := []RenumberedLedgerEntry{{
		LedgerVersion:  "441_project_instructions",
		DiskStem:       "901_project_instructions",
		NewStemApplied: true,
	}}
	if !reflect.DeepEqual(report.Renumbered, want) {
		t.Fatalf("Renumbered = %+v, want %+v", report.Renumbered, want)
	}
	if len(report.Unknown) != 0 {
		t.Fatalf("Unknown = %v, want none", report.Unknown)
	}
	if len(report.Pending) != 0 {
		t.Fatalf("Pending = %v, want none", report.Pending)
	}
}

// When only the stale stem is recorded, the row must be re-pointed at the new
// number rather than deleted: deleting it would make the runner replay the
// migration under its new stem, which is exactly how 901_project_instructions
// dropped live prompt content in RUYI-212.
func TestReconcileLedgerReportsRenumberedEntryWhoseNewStemIsNotApplied(t *testing.T) {
	report := ReconcileLedger(
		[]string{"901_project_instructions"},
		[]string{"441_project_instructions"},
	)

	want := []RenumberedLedgerEntry{{
		LedgerVersion:  "441_project_instructions",
		DiskStem:       "901_project_instructions",
		NewStemApplied: false,
	}}
	if !reflect.DeepEqual(report.Renumbered, want) {
		t.Fatalf("Renumbered = %+v, want %+v", report.Renumbered, want)
	}
	if len(report.Pending) != 0 {
		t.Fatalf("Pending = %v, want none — the migration is applied, just under the old number", report.Pending)
	}
}

// A ledger row whose name part matches nothing on disk cannot be a renumber of
// anything this checkout ships. On a long-lived shared database that is the
// normal footprint of another branch's migration, so it is reported without
// failing the check.
func TestReconcileLedgerReportsUnmatchedLedgerRowAsUnknown(t *testing.T) {
	report := ReconcileLedger(
		[]string{"928_something"},
		[]string{"928_something", "929_prompt_quiz"},
	)

	if !reflect.DeepEqual(report.Unknown, []string{"929_prompt_quiz"}) {
		t.Fatalf("Unknown = %v, want [929_prompt_quiz]", report.Unknown)
	}
	if len(report.Renumbered) != 0 {
		t.Fatalf("Renumbered = %+v, want none", report.Renumbered)
	}
}

func TestReconcileLedgerReportsDiskStemsWithNoLedgerRowAsPending(t *testing.T) {
	report := ReconcileLedger(
		[]string{"928_applied", "933_new"},
		[]string{"928_applied"},
	)

	if !reflect.DeepEqual(report.Pending, []string{"933_new"}) {
		t.Fatalf("Pending = %v, want [933_new]", report.Pending)
	}
	if len(report.Renumbered) != 0 || len(report.Unknown) != 0 {
		t.Fatalf("report = %+v, want only Pending", report)
	}
}

func TestReconcileLedgerIsCleanWhenLedgerMatchesDisk(t *testing.T) {
	report := ReconcileLedger(
		[]string{"928_applied", "933_new"},
		[]string{"928_applied", "933_new"},
	)

	if report.HasMismatch() {
		t.Fatalf("report = %+v, want no mismatch", report)
	}
	if len(report.Pending) != 0 {
		t.Fatalf("Pending = %v, want none", report.Pending)
	}
}

// Only renumbered rows are a mismatch. Unknown rows and pending files are
// reported but must not fail the gate, or every developer database that ever
// checked out another branch would be permanently red.
func TestReconcileLedgerHasMismatchOnlyForRenumberedRows(t *testing.T) {
	if got := ReconcileLedger([]string{"901_a"}, []string{"441_a"}); !got.HasMismatch() {
		t.Fatal("renumbered row must count as a mismatch")
	}
	if got := ReconcileLedger([]string{"901_a"}, []string{"901_a", "902_gone"}); got.HasMismatch() {
		t.Fatal("unknown ledger row must not count as a mismatch")
	}
	if got := ReconcileLedger([]string{"901_a", "902_b"}, []string{"901_a"}); got.HasMismatch() {
		t.Fatal("pending migration must not count as a mismatch")
	}
}

// The repair statements are what the renumbering procedure tells an operator to
// paste. They must re-point the surviving row and remove the duplicate, never
// touch anything else, and stay parameter-free so they can be copied as-is.
func TestLedgerRepairStatementsRewriteRunRecordsInPlace(t *testing.T) {
	report := ReconcileLedger(
		[]string{"901_project_instructions", "902_user_admin_state"},
		[]string{"441_project_instructions", "441_user_admin_state", "901_project_instructions"},
	)

	got := report.RepairStatements()
	want := []string{
		`DELETE FROM schema_migrations WHERE version = '441_project_instructions';`,
		`UPDATE schema_migrations SET version = '902_user_admin_state' WHERE version = '441_user_admin_state';`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepairStatements() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Renumbering is only mechanically reconcilable while the name part identifies
// a migration on its own. Two files sharing a name part would make a stale
// ledger row ambiguous, so the invariant is gated at the file level.
func TestMigrationNamePartsAreGloballyUnique(t *testing.T) {
	files := migrationFilesForLint(t, "*.up.sql")

	stemsByName := make(map[string][]string)
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file), ".up.sql")
		_, name, ok := splitMigrationStem(stem)
		if !ok {
			t.Fatalf("migration %s does not start with a numeric prefix followed by underscore", stem)
		}
		stemsByName[name] = append(stemsByName[name], stem)
	}

	for name, stems := range stemsByName {
		if len(stems) > 1 {
			t.Errorf("migration name part %q is used by %v; rename one of them — the renumbering procedure identifies a schema_migrations row by its name part, and a duplicate makes that lookup ambiguous", name, stems)
		}
	}
}
