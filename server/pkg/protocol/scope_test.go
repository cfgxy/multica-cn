package protocol

import (
	"strings"
	"testing"
)

func TestParseTaskDirectoryScopeAccepts(t *testing.T) {
	t.Parallel()
	scopes, err := ParseTaskDirectoryScope([]byte(`[{"resource_id":" r1 ","subdirectories":["/a/b/","a\\c"]}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(scopes) != 1 || scopes[0].ResourceID != "r1" {
		t.Fatalf("resource id must be trimmed, got %+v", scopes)
	}
	if got := scopes[0].Subdirectories; len(got) != 2 || got[0] != "a/b" || got[1] != "a/c" {
		t.Fatalf("subdirectories must normalize, got %v", got)
	}
	for _, raw := range []string{"", "null"} {
		scopes, err := ParseTaskDirectoryScope([]byte(raw))
		if err != nil || scopes != nil {
			t.Fatalf("undeclared column %q must parse to nil, got %v %v", raw, scopes, err)
		}
	}
}

func TestParseTaskDirectoryScopeRejects(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, raw string }{
		{"malformed json", `{"nope"`},
		{"unknown key", `[{"resource_id":"r1","subdirectories":["a"],"extra":1}]`},
		{"trailing data", `[{"resource_id":"r1","subdirectories":["a"]}] []`},
		{"empty resource id", `[{"resource_id":" ","subdirectories":["a"]}]`},
		{"no subdirectories", `[{"resource_id":"r1","subdirectories":[]}]`},
		{"empty segment subdir", `[{"resource_id":"r1","subdirectories":["a//b"]}]`},
		{"traversal subdir", `[{"resource_id":"r1","subdirectories":["a/../b"]}]`},
	}
	for _, tc := range cases {
		if _, err := ParseTaskDirectoryScope([]byte(tc.raw)); err == nil {
			t.Errorf("%s: must be rejected, got nil error", tc.name)
		}
	}
}

// TestParseTaskDirectoryScopeRejectsDuplicates pins the two claim-side
// rejections that keep the re-parse symmetric with the save side: a
// subdirectory declared twice within one entry, and a resource named by more
// than one entry. The claim path re-runs this parser to catch older or
// non-API writers, so it must reject exactly what the save side rejects.
func TestParseTaskDirectoryScopeRejectsDuplicates(t *testing.T) {
	t.Parallel()
	if _, err := ParseTaskDirectoryScope([]byte(`[{"resource_id":"r1","subdirectories":["a","a"]}]`)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate subdirectory within one entry must be rejected, got %v", err)
	}
	// Same spelling after normalization counts as a duplicate too.
	if _, err := ParseTaskDirectoryScope([]byte(`[{"resource_id":"r1","subdirectories":["a/b","a/b/"]}]`)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("subdirectories equal after normalization must be rejected, got %v", err)
	}
	if _, err := ParseTaskDirectoryScope([]byte(`[{"resource_id":"r1","subdirectories":["a"]},{"resource_id":"r1","subdirectories":["b"]}]`)); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("resource declared by two entries must be rejected, got %v", err)
	}
	// Distinct resources with distinct subdirectories stay fine.
	if _, err := ParseTaskDirectoryScope([]byte(`[{"resource_id":"r1","subdirectories":["a"]},{"resource_id":"r2","subdirectories":["b"]}]`)); err != nil {
		t.Fatalf("distinct resources must parse, got %v", err)
	}
}
