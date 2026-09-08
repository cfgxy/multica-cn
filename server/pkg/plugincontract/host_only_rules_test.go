package plugincontract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// The rules the published JSON Schema deliberately does not express.
//
// `packages/plugin-sdk/manifest.schema.json` is what a third party points their
// editor at, and every one of these manifests passes it — the schema suite
// asserts exactly that. What makes "the shape is right, the publish may still
// fail" an honest statement rather than a disclaimer is this test: each sample
// must be refused HERE, for the rule its file name names.
//
// The pairing is the point. A rule that quietly left the host would leave a
// manifest accepted by both sides, and the two suites are the only place that
// difference shows up.
func TestHostRejectsWhatTheSchemaCannotExpress(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "plugins", "invalid-manifests", "host-only")

	// Keyed by file so a rejection for an unrelated reason — a typo in a
	// fixture, say — is not counted as the rule still being enforced.
	cases := map[string]string{
		"contributes-over-the-total-limit.json":      "contributes must not exceed 64 entries",
		"skill-entry-does-not-match-its-key.json":    "entry must be \"skills/triage/SKILL.md\"",
		"hook-url-outside-the-net-scopes.json":       "is not covered by a net: scope",
		"event-without-the-matching-read-scope.json": "comments:read",
		"schedule-more-often-than-five-minutes.json": "must not run more often than every five minutes",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read host-only directory: %v", err)
	}
	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found++
		want, ok := cases[entry.Name()]
		if !ok {
			t.Fatalf("%s has no expected rejection; a sample nothing asserts about is a sample that proves nothing", entry.Name())
		}
		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, _, err = plugincontract.ParseManifest(raw)
			if err == nil {
				t.Fatal("the host accepted a manifest the schema was documented as unable to catch")
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("rejected for the wrong reason:\n got: %v\nwant it to mention: %s", err, want)
			}
		})
	}
	if found != len(cases) {
		t.Fatalf("found %d host-only samples, expected %d", found, len(cases))
	}
}
