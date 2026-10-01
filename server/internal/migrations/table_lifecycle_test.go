package migrations

import "testing"

// RequiredTables drives the test-suite precheck's physical spot check
// (RUYI-311): a ledger row whose migration is recorded as applied while the
// table it creates is physically missing made handler tests fail as 500s in
// unrelated cases. The extraction must see through quoted identifiers
// ("user") and exclude tables a later up migration drops again.
func TestRequiredTablesExtraction(t *testing.T) {
	required, err := RequiredTables()
	if err != nil {
		t.Fatalf("extract required tables: %v", err)
	}

	for _, table := range []string{"user", "workspace", "member", "issue"} {
		version, ok := required[table]
		if !ok {
			t.Fatalf("table %s missing from required tables", table)
		}
		if version != "001_init" {
			t.Fatalf("table %s created by %s, want 001_init", table, version)
		}
	}

	// Tables dropped again by a later up migration must not be required:
	// they legitimately do not exist in a fully migrated database.
	for _, table := range []string{"daemon_pairing_session", "runtime_usage", "task_usage_daily"} {
		if version, ok := required[table]; ok {
			t.Fatalf("dropped table %s (created by %s) must not be required", table, version)
		}
	}

	// Prose in a migration comment must not be parsed as DDL: migration 442
	// mentions "CREATE TABLE IF NOT EXISTS" mid-sentence (RUYI-322).
	if version, ok := required["if"]; ok {
		t.Fatalf("comment prose parsed as a table named %q (created by %s)", "if", version)
	}

	// A table dropped and re-created by a still later migration is required
	// again: last lifecycle event in apply order wins.
	for _, table := range []string{"lark_inbound_message_dedup", "plugin_installation"} {
		if _, ok := required[table]; !ok {
			t.Fatalf("re-created table %s must be required", table)
		}
	}

	if len(required) < 100 {
		t.Fatalf("suspiciously few required tables (%d); extraction is probably broken", len(required))
	}
}
