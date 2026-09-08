package service

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A secret-typed configuration field is write-only: an administrator sets it,
// and from then on it reaches exactly one place — the `Authorization` header of
// the `mcp` transport, and only for the field named `<hookKey>_credential`.
//
// The surface bridge and the http hook body are the two paths where a
// regression would be silent and expensive: both carry `config` to a third
// party, and a secret leaking into either hands the value to whoever holds the
// endpoint. Neither has any other test asserting the absence, so it is asserted
// here rather than left to the code's good intentions.
//
// examples/plugins/oncall-handoff is written against this guarantee and refuses
// to run if it is broken.

const secretConfigManifest = `{
  "manifest_version": 1,
  "key": "com.example.oncall-handoff",
  "name": "On-call Handoff",
  "version": "1.0.0",
  "author": {"name": "Example"},
  "scopes": ["issues:read"],
  "config": {
    "rota_name":            {"type": "string", "label": "Rota"},
    "handoff_window_hours": {"type": "number", "label": "Window"},
    "rota_token":           {"type": "secret", "label": "Rota token"},
    "roster_credential":    {"type": "secret", "label": "Roster token"}
  }
}`

// A stored installation whose config column has, wrongly, picked up secrets.
// SetConfig routes them to the encrypted table so this should never occur —
// which is exactly why the filter is written against the manifest, and why the
// fixture reproduces the state it exists to survive.
func installationWithLeakedSecrets(t *testing.T) db.PluginInstallation {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"rota_name":            "platform-primary",
		"handoff_window_hours": 12,
		"rota_token":           "rota-live-abcdefghijklmnop",
		"roster_credential":    "roster-live-qrstuvwxyz012345",
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return db.PluginInstallation{
		Manifest: []byte(secretConfigManifest),
		Config:   config,
	}
}

func TestHookBodyConfigCarriesNoSecret(t *testing.T) {
	values := nonSecretConfig(installationWithLeakedSecrets(t))

	if got, want := values["rota_name"], "platform-primary"; got != want {
		t.Fatalf("non-secret config must still reach the handler: got %v, want %v", got, want)
	}
	if _, present := values["handoff_window_hours"]; !present {
		t.Fatal("non-secret config must still reach the handler: handoff_window_hours is missing")
	}
	for _, key := range []string{"rota_token", "roster_credential"} {
		if _, present := values[key]; present {
			t.Fatalf("secret-typed field %q reached the hook body", key)
		}
	}

	// Serialized, because absence from the map is not the same as absence from
	// the wire once anything downstream re-encodes it.
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal filtered config: %v", err)
	}
	for _, needle := range []string{"rota-live-", "roster-live-"} {
		if strings.Contains(string(encoded), needle) {
			t.Fatalf("a secret value survived into the serialized hook body: %s", encoded)
		}
	}
}

// A key the manifest never declared is dropped too. The manifest is the
// allow-list: a value that arrived by some other route has no declared type, so
// there is nothing to say it is safe to forward.
func TestUndeclaredConfigKeyIsNotForwarded(t *testing.T) {
	config, err := json.Marshal(map[string]any{
		"rota_name": "platform-primary",
		"smuggled":  "whatever-this-is",
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	values := nonSecretConfig(db.PluginInstallation{
		Manifest: []byte(secretConfigManifest),
		Config:   config,
	})

	if _, present := values["smuggled"]; present {
		t.Fatal("an undeclared config key was forwarded to the handler")
	}
	if _, present := values["rota_name"]; !present {
		t.Fatal("dropping the undeclared key must not drop the declared ones")
	}
}

// Without a readable manifest there is no way to tell which keys are secret,
// so the only safe answer is to send none of them.
func TestUnreadableManifestForwardsNoConfig(t *testing.T) {
	config, err := json.Marshal(map[string]any{"rota_token": "rota-live-abcdefghijklmnop"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	values := nonSecretConfig(db.PluginInstallation{
		Manifest: []byte("not json"),
		Config:   config,
	})

	if len(values) != 0 {
		t.Fatalf("an unreadable manifest must forward nothing, got %v", values)
	}
}

// The upgrade case, which is where the write-only contract used to break.
//
// A plugin ships a field as `string`, an administrator sets it, and the next
// version reclassifies it as `secret` — the ordinary way an author fixes having
// shipped a credential as plain configuration. Pruning on "does the key still
// exist" kept the plaintext in `installation.Config`, and every read endpoint
// returns that column, so the upgrade meant to start protecting the value went
// on serving it in the clear to anyone who could open the settings page.
func TestUpgradeDropsAValueWhoseFieldBecameSecret(t *testing.T) {
	const retyped = `{
	  "manifest_version": 1,
	  "key": "com.example.retype",
	  "name": "Retype",
	  "version": "2.0.0",
	  "author": {"name": "Example"},
	  "scopes": ["issues:read"],
	  "config": {
	    "rota_name":  {"type": "string", "label": "Rota"},
	    "rota_token": {"type": "secret", "label": "Rota token"}
	  }
	}`
	// Read the way an upgrade reads it: the stored consented snapshot, not a
	// fresh strict parse of an author's upload.
	manifest, err := ParseInstallationManifest(db.PluginInstallation{Manifest: []byte(retyped)})
	if err != nil {
		t.Fatalf("parse retyped manifest: %v", err)
	}

	// v1 stored both as plain values, because at v1 both were plain.
	pruned := pruneConfig([]byte(`{"rota_name":"platform-primary","rota_token":"rota-live-abcdefghijklmnop"}`), manifest)

	if strings.Contains(string(pruned), "rota-live-") {
		t.Fatalf("a value retyped to secret survived the upgrade as plaintext: %s", pruned)
	}
	var values map[string]any
	if err := json.Unmarshal(pruned, &values); err != nil {
		t.Fatalf("decode pruned config: %v", err)
	}
	if values["rota_name"] != "platform-primary" {
		t.Fatalf("pruning dropped a field that is still plain: %s", pruned)
	}
}

// The exit gate, asserted separately from the pruning that should make it
// unnecessary. NonSecretStoredConfig is what both the hook body and the
// settings API response run stored config through, so a row that predates the
// pruning fix — or any future write path that forgets — still cannot echo a
// secret to a browser.
func TestStoredSecretResidueIsNotReturnedByAReadPath(t *testing.T) {
	manifest, err := ParseInstallationManifest(db.PluginInstallation{Manifest: []byte(secretConfigManifest)})
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	values := NonSecretStoredConfig(installationWithLeakedSecrets(t).Config, manifest)

	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal filtered config: %v", err)
	}
	for _, needle := range []string{"rota-live-", "roster-live-"} {
		if strings.Contains(string(encoded), needle) {
			t.Fatalf("a read path returned a stored secret: %s", encoded)
		}
	}
	if values["rota_name"] != "platform-primary" {
		t.Fatalf("filtering dropped a non-secret value: %v", values)
	}
}

// The third exit, and the one that reaches third-party code.
//
// Filtering the hook body and the settings response left the surface bridge
// reading `installation.Config` straight out of the row. A stored secret — a
// row written before secrets were split off, or by any write path that forgets
// — was therefore handed to the iframe verbatim, which is the plugin's own
// JavaScript. This drives the real BuildPluginContext rather than the helper:
// the helper being correct was never the thing in doubt.
func TestSurfaceContextCarriesNoSecret(t *testing.T) {
	service := &PluginService{}
	context := service.BuildPluginContext(
		PluginActionCaller{Installation: installationWithLeakedSecrets(t)},
		db.Workspace{Name: "Platform", Slug: "platform"},
		nil,
		nil,
	)

	for _, key := range []string{"rota_token", "roster_credential"} {
		if _, present := context.Config[key]; present {
			t.Fatalf("secret-typed field %q reached the surface context", key)
		}
	}
	if context.Config["rota_name"] != "platform-primary" {
		t.Fatalf("non-secret config must still reach the surface: %v", context.Config)
	}

	// Serialized, because the payload is JSON by the time it crosses into the
	// iframe and absence from a map is not absence from the wire.
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	for _, needle := range []string{"rota-live-", "roster-live-"} {
		if strings.Contains(string(encoded), needle) {
			t.Fatalf("a secret value survived into the surface context payload: %s", encoded)
		}
	}
}

// Same reasoning as the hook body: without a readable manifest nothing can say
// which keys are secret, so the surface gets none of them.
func TestSurfaceContextSendsNoConfigWhenTheManifestIsUnreadable(t *testing.T) {
	service := &PluginService{}
	context := service.BuildPluginContext(
		PluginActionCaller{Installation: db.PluginInstallation{
			Manifest: []byte("not json"),
			Config:   []byte(`{"rota_token":"rota-live-abcdefghijklmnop"}`),
		}},
		db.Workspace{Name: "Platform", Slug: "platform"},
		nil,
		nil,
	)

	if len(context.Config) != 0 {
		t.Fatalf("an unreadable manifest must send no config to a surface, got %v", context.Config)
	}
}
