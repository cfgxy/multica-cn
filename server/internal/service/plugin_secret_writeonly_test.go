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
