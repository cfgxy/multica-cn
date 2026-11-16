package migrations

import (
	"path/filepath"
	"testing"
)

func TestMigrationOrderKeySortsNumericPrefixes(t *testing.T) {
	if got, want := migrationOrderKey("996_knowledge_entry_version_provenance.up.sql") < migrationOrderKey("1000_knowledge_scan_fencing.up.sql"), true; got != want {
		t.Fatalf("numeric migration order = %t, want %t", got, want)
	}

	if got, want := migrationOrderKey("1003_runtime_model_call_budget.up.sql") < migrationOrderKey("1003_task_usage_model_call_count.up.sql"), true; got != want {
		t.Fatalf("same-prefix migration order = %t, want %t", got, want)
	}
}

func TestMigrationOrderKeyKeepsNonNumericNamesSortable(t *testing.T) {
	if got, want := migrationOrderKey("legacy.up.sql"), "legacy.up.sql"; got != want {
		t.Fatalf("non-numeric migration key = %q, want %q", got, want)
	}
}

func TestFilesUsesNumericMigrationOrder(t *testing.T) {
	up, err := Files("up")
	if err != nil {
		t.Fatalf("Files(up): %v", err)
	}
	down, err := Files("down")
	if err != nil {
		t.Fatalf("Files(down): %v", err)
	}

	up996 := indexOfMigration(up, "1003_task_usage_model_call_count.up.sql")
	up1000 := indexOfMigration(up, "1006_task_usage_model_call_totals.up.sql")
	if up996 < 0 || up1000 < 0 || up996 >= up1000 {
		t.Fatalf("up migration order has 996 at %d and 1000 at %d", up996, up1000)
	}

	down996 := indexOfMigration(down, "1003_task_usage_model_call_count.down.sql")
	down1000 := indexOfMigration(down, "1006_task_usage_model_call_totals.down.sql")
	if down996 < 0 || down1000 < 0 || down1000 >= down996 {
		t.Fatalf("down migration order has 1000 at %d and 996 at %d", down1000, down996)
	}
}

func indexOfMigration(files []string, name string) int {
	for i, file := range files {
		if filepath.Base(file) == name {
			return i
		}
	}
	return -1
}
