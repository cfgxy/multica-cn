package migrations

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilesOrdersNumericSequenceAndReversesDown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "migrations")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"098_legacy",
		"995_knowledge_lifecycle",
		"999_knowledge_operation",
		"999_knowledge_operation_claim",
		"999_knowledge_operation_index",
		"1000_knowledge_fencing",
		"1001_next",
	}
	for _, name := range want {
		for _, direction := range []string{"up", "down"} {
			if err := os.WriteFile(filepath.Join(dir, name+"."+direction+".sql"), []byte("SELECT 1;"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Chdir(root)
	for _, direction := range []string{"up", "down"} {
		files, err := Files(direction)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(files))
		for i, file := range files {
			got[i] = ExtractVersion(file)
		}
		expected := append([]string(nil), want...)
		if direction == "down" {
			for i, j := 0, len(expected)-1; i < j; i, j = i+1, j-1 {
				expected[i], expected[j] = expected[j], expected[i]
			}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s files = %v, want %v", direction, got, expected)
		}
	}
}
