package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A required field left unset must take the whole install with it.
//
// Installing is one transaction because it writes in four places: the
// installation row, the ciphertext of every secret typed on the consent screen,
// a workspace skill per skill resource, and a schedule row per scheduled hook.
// The required-config check runs against the MERGED state — what is stored plus
// what this request supplies — and it runs inside that transaction, so a
// rejection has to leave all four empty. The failure it exists to prevent is
// the visible one: a plugin listed as installed, its credential missing, its
// skill already offered to agents and its cron already claimed by the runner.
//
// TestPluginInstallRollsBackOnInvalidConsentConfig in plugin_test.go covers the
// same rollback for an unknown field. This one covers the required-field gate,
// on a manifest that actually contributes to all four tables.

const rollbackPluginManifest = `{
  "manifest_version": 1,
  "key": "com.example.rollback",
  "name": "Rollback",
  "version": "1.0.0",
  "author": { "name": "example" },
  "scopes": ["issues:read", "net:example.com"],
  "config": {
    "repo": { "type": "string", "label": "Repo", "required": true },
    "token": { "type": "secret", "label": "Token", "required": true }
  },
  "contributes": {
    "resources": [{ "type": "skill", "key": "triage", "entry": "skills/triage/SKILL.md" }],
    "hooks": [{
      "key": "heartbeat",
      "name": "Heartbeat",
      "description": "Send a periodic heartbeat.",
      "triggers": ["schedule"],
      "schedule": { "cron": "*/5 * * * *", "timezone": "UTC" },
      "transport": { "type": "http", "url": "https://example.com/hooks/heartbeat" }
    }]
  }
}`

// pluginInstallFootprint counts every table an install writes to, so an
// assertion covers the whole transaction rather than the one row a reviewer
// happened to think of.
type pluginInstallFootprint struct {
	installations int
	secrets       int
	skills        int
	schedules     int
}

func countPluginInstallFootprint(t *testing.T) pluginInstallFootprint {
	t.Helper()
	ctx := context.Background()
	var footprint pluginInstallFootprint
	count := func(query string, target *int) {
		t.Helper()
		if err := testPool.QueryRow(ctx, query, testWorkspaceID).Scan(target); err != nil {
			t.Fatalf("count (%s): %v", query, err)
		}
	}
	count(`SELECT COUNT(*) FROM plugin_installation WHERE workspace_id = $1`, &footprint.installations)
	// plugin_secret has no workspace of its own; counting the whole table is
	// what catches a row orphaned by a rolled-back installation, which is
	// exactly the leak a join through plugin_installation would hide.
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM plugin_secret`).Scan(&footprint.secrets); err != nil {
		t.Fatalf("count secrets: %v", err)
	}
	count(`SELECT COUNT(*) FROM skill WHERE workspace_id = $1 AND plugin_installation_id IS NOT NULL`, &footprint.skills)
	count(`SELECT COUNT(*) FROM plugin_hook_schedule WHERE workspace_id = $1`, &footprint.schedules)
	return footprint
}

func TestPluginInstallRollsBackWhenARequiredFieldIsMissing(t *testing.T) {
	withPluginsV1Flag(t, testHandler, true)
	cleanupPluginInstallations(t)
	root := t.TempDir()
	versionID := withLocalPluginSourceIn(t, root, rollbackPluginManifest)

	cases := []struct {
		name    string
		config  map[string]any
		missing string
	}{
		// The secret is supplied, so the install gets far enough to encrypt and
		// write it before the plain field is found missing. Its ciphertext going
		// back with the rest is the point.
		{name: "a required plain field", config: map[string]any{"token": "sk-rollback-secret"}, missing: "repo"},
		// The mirror case: a stored plain value must not make a missing secret
		// look satisfied, and the skill and schedule rows must not survive it.
		{name: "a required secret", config: map[string]any{"repo": "multica-ai/multica"}, missing: "token"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := countPluginInstallFootprint(t)

			body, _ := json.Marshal(map[string]any{
				"version_id":     versionID,
				"granted_scopes": []string{"issues:read", "net:example.com"},
				"config":         testCase.config,
			})
			recorder := httptest.NewRecorder()
			testHandler.InstallPlugin(recorder,
				pluginHandlerRequest(http.MethodPost, "/plugins", body, map[string]string{"id": testWorkspaceID}))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), testCase.missing) {
				t.Fatalf("the rejection does not name the field the operator has to fill in: %s", recorder.Body.String())
			}
			// A secret must not be echoed on the way out, even in the error that
			// says the install failed.
			if strings.Contains(recorder.Body.String(), "sk-rollback-secret") {
				t.Fatalf("the rejection quoted the secret: %s", recorder.Body.String())
			}

			after := countPluginInstallFootprint(t)
			if after != before {
				t.Fatalf("a rejected install left rows behind: before=%+v after=%+v", before, after)
			}
		})
	}

	// The gate has to be a gate, not a wall: the same manifest installs once
	// both required fields are there. Without this the test above would pass on
	// a handler that refused every install.
	body, _ := json.Marshal(map[string]any{
		"version_id":     versionID,
		"granted_scopes": []string{"issues:read", "net:example.com"},
		"config":         map[string]any{"repo": "multica-ai/multica", "token": "sk-rollback-secret"},
	})
	recorder := httptest.NewRecorder()
	testHandler.InstallPlugin(recorder,
		pluginHandlerRequest(http.MethodPost, "/plugins", body, map[string]string{"id": testWorkspaceID}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("a fully configured install was refused: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	complete := countPluginInstallFootprint(t)
	if complete.installations != 1 || complete.secrets != 1 || complete.skills != 1 || complete.schedules != 1 {
		t.Fatalf("a successful install wrote %+v; the rollback assertions above compare against these four tables", complete)
	}

	// cleanupPluginInstallations does not reach the skill table — a plugin's
	// skills leave through uninstall, which is the path this suite should use
	// anyway rather than leaving a row for the next test to count.
	var installed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &installed); err != nil {
		t.Fatalf("decode installation: %v", err)
	}
	uninstall := httptest.NewRecorder()
	testHandler.UninstallPlugin(uninstall, pluginHandlerRequest(http.MethodDelete, "/plugins", nil, map[string]string{
		"id": testWorkspaceID, "installationId": installed.ID,
	}))
	if uninstall.Code != http.StatusNoContent {
		t.Fatalf("uninstall status=%d body=%s", uninstall.Code, uninstall.Body.String())
	}
	if remaining := countPluginInstallFootprint(t); remaining != (pluginInstallFootprint{}) {
		t.Fatalf("uninstall left %+v behind", remaining)
	}
}
