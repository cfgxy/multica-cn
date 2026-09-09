package service

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// The secret scanner is not the only publish path that quotes author-supplied
// text.
//
// A bundle is PARSED before it is scanned, and the parser names the entry at
// fault so the author can fix it — `plugin package is missing "ui/....js"`. That
// entry name comes from the manifest, and a manifest may name a file that never
// made it into the archive, so a credential pasted into a path reaches the
// rejection through a route the scanner never sees: on this path the scan has
// not run yet, and never will.
//
// The values below are synthetic strings in the providers' published formats.
// None of them authenticates anywhere.

const redactionSecret = "AKIAQQQQQQQQQQQQQQQQ"

func redactionManifest(entry string) string {
	return `{
  "manifest_version": 1,
  "key": "com.example.redaction",
  "name": "Redaction",
  "version": "1.0.0",
  "author": { "name": "example" },
  "scopes": ["issues:read"],
  "contributes": {
    "surfaces": [{ "key": "panel", "type": "issue_panel", "name": "Panel", "entry": "` + entry + `" }]
  }
}`
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

// requireRedactedPublishFailure asserts the one property this whole path exists
// for: the author still learns where to look, and the credential is not in the
// answer.
func requireRedactedPublishFailure(t *testing.T, err error, secret, wantLocation string) {
	t.Helper()
	if err == nil {
		t.Fatal("publish accepted a bundle it cannot parse")
	}
	var pluginErr *PluginError
	if !asPluginError(err, &pluginErr) || pluginErr.Kind != PluginErrorInvalid {
		t.Fatalf("kind = %v, want %q so publishing answers 400 rather than 500", err, PluginErrorInvalid)
	}
	message := pluginErr.Error()
	if strings.Contains(message, secret) {
		t.Fatalf("the rejection quotes the credential from the entry name: %q", message)
	}
	if !strings.Contains(message, wantLocation) {
		t.Fatalf("the rejection redacted the location into uselessness: %q", message)
	}
}

// The manifest names a file the archive does not carry. `buildBundle` fails
// before `scanBundleForSecrets` is ever reached.
func TestPublishDoesNotEchoASecretEntryNameFromAMissingFile(t *testing.T) {
	entry := "ui/" + redactionSecret + ".js"
	archive := zipArchive(t, map[string]string{
		plugincontract.ManifestFilename: redactionManifest(entry),
	})

	service := &PluginService{Host: plugincontract.HostCapabilities()}
	_, err := service.PublishBundle(context.Background(), pgtype.UUID{}, pgtype.UUID{}, archive)
	requireRedactedPublishFailure(t, err, redactionSecret, "ui/[redacted].js")
}

// The file is present and empty, which is a different parser branch —
// `validateSurfaceScript` — and quotes the same entry name.
func TestPublishDoesNotEchoASecretEntryNameFromAnEmptySurface(t *testing.T) {
	entry := "ui/" + redactionSecret + ".js"
	archive := zipArchive(t, map[string]string{
		plugincontract.ManifestFilename: redactionManifest(entry),
		entry:                           "   \n",
	})

	service := &PluginService{Host: plugincontract.HostCapabilities()}
	_, err := service.PublishBundle(context.Background(), pgtype.UUID{}, pgtype.UUID{}, archive)
	requireRedactedPublishFailure(t, err, redactionSecret, "ui/[redacted].js")
}

// A surface that is not valid JavaScript is the branch whose message carries the
// parser's own wrapped error, so redaction has to survive the `%w` chain too.
func TestPublishDoesNotEchoASecretEntryNameFromAnUnparseableSurface(t *testing.T) {
	entry := "ui/" + redactionSecret + ".js"
	archive := zipArchive(t, map[string]string{
		plugincontract.ManifestFilename: redactionManifest(entry),
		entry:                           "function ( {",
	})

	service := &PluginService{Host: plugincontract.HostCapabilities()}
	_, err := service.PublishBundle(context.Background(), pgtype.UUID{}, pgtype.UUID{}, archive)
	requireRedactedPublishFailure(t, err, redactionSecret, "ui/[redacted].js")
}

// The local development channel parses the same manifest through a different
// reader, and returns its own wrapper. It leaks identically if only one of the
// two is redacted.
func TestPublishLocalDoesNotEchoASecretEntryName(t *testing.T) {
	entry := "ui/" + redactionSecret + ".js"
	root := t.TempDir()
	source := filepath.Join(root, "redaction")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("create local source: %v", err)
	}
	manifestPath := filepath.Join(source, plugincontract.ManifestFilename)
	if err := os.WriteFile(manifestPath, []byte(redactionManifest(entry)), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	service := &PluginService{LocalDir: root, Host: plugincontract.HostCapabilities()}
	_, err := service.PublishLocalBundle(context.Background(), pgtype.UUID{}, pgtype.UUID{}, "redaction")
	requireRedactedPublishFailure(t, err, redactionSecret, "ui/[redacted].js")
}
