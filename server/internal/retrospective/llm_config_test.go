package retrospective

// Tests for the workspace LLM resolution chain (RUYI-552): UI-saved config
// first, deployment env defaults as per-field fallback, none → not effective.
// The sealed key never round-trips in plaintext outside the resolver, and a
// run whose LLM errors must never store the resolved key in the run record.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func testSecretBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	for i := range key {
		key[i] = byte(i + 3)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	return box
}

func sealWSKey(t *testing.T, box *secretbox.Box, plaintext string) []byte {
	t.Helper()
	sealed, err := box.Seal([]byte(plaintext))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

func saveWSLLM(t *testing.T, wsID, baseURL, model string, keyEnc []byte, hint string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`UPDATE retrospective_config SET llm_base_url=$2, llm_model=$3, llm_api_key_encrypted=$4, llm_api_key_hint=$5 WHERE workspace_id=$1`,
		wsID, baseURL, model, keyEnc, hint)
	if err != nil {
		t.Fatalf("save workspace llm config: %v", err)
	}
}

func resolveTest(t *testing.T, box *secretbox.Box, wsID string, env DeploymentLLM) ResolvedLLM {
	t.Helper()
	return ResolveWorkspaceLLM(context.Background(), db.New(testPool), box, wsID, env)
}

var envAll = DeploymentLLM{APIKey: "env-key-9999", BaseURL: "https://env.example.com/v1", DefaultModel: "env-model"}

// TestResolveWorkspaceLLMPriority: workspace fields win per-field; unsaved
// fields fall back to the deployment defaults; with nothing saved anywhere
// the resolution is not effective.
func TestResolveWorkspaceLLMPriority(t *testing.T) {
	box := testSecretBox(t)
	wsID := retroFixture(t, "")

	// Nothing saved → pure deployment fallback.
	got := resolveTest(t, box, wsID, envAll)
	if !got.Effective {
		t.Fatalf("env-only config must be effective: %+v", got)
	}
	if got.Source != LLMSourceDeployment {
		t.Fatalf("source = %q, want %q", got.Source, LLMSourceDeployment)
	}
	if got.APIKey != envAll.APIKey || got.BaseURL != envAll.BaseURL || got.Model != envAll.DefaultModel {
		t.Fatalf("env fallback fields wrong: %+v", got)
	}
	if got.KeySource != LLMSourceDeployment || got.URLSource != LLMSourceDeployment || got.ModelSource != LLMSourceDeployment {
		t.Fatalf("env fallback sources wrong: %+v", got)
	}

	// Full workspace config overrides every field.
	saveWSLLM(t, wsID, "https://ws.example.com/v1", "ws-model", sealWSKey(t, box, "ws-key-0000"), "0000")
	got = resolveTest(t, box, wsID, envAll)
	if got.APIKey != "ws-key-0000" || got.BaseURL != "https://ws.example.com/v1" || got.Model != "ws-model" {
		t.Fatalf("workspace override fields wrong: %+v", got)
	}
	if got.Source != LLMSourceWorkspace {
		t.Fatalf("source = %q, want %q", got.Source, LLMSourceWorkspace)
	}
	if got.KeySource != LLMSourceWorkspace || got.URLSource != LLMSourceWorkspace || got.ModelSource != LLMSourceWorkspace {
		t.Fatalf("workspace sources wrong: %+v", got)
	}

	// Per-field fallback: only the base URL saved → key and model come from
	// the deployment defaults.
	saveWSLLM(t, wsID, "https://ws.example.com/v1", "", nil, "")
	got = resolveTest(t, box, wsID, envAll)
	if got.APIKey != envAll.APIKey || got.BaseURL != "https://ws.example.com/v1" || got.Model != envAll.DefaultModel {
		t.Fatalf("per-field fallback fields wrong: %+v", got)
	}
	if got.KeySource != LLMSourceDeployment || got.URLSource != LLMSourceWorkspace || got.ModelSource != LLMSourceDeployment {
		t.Fatalf("per-field fallback sources wrong: %+v", got)
	}
	// A saved workspace field makes the coarse source "workspace".
	if got.Source != LLMSourceWorkspace {
		t.Fatalf("mixed source = %q, want %q", got.Source, LLMSourceWorkspace)
	}

	// Clearing everything returns to the pure deployment fallback.
	saveWSLLM(t, wsID, "", "", nil, "")
	got = resolveTest(t, box, wsID, envAll)
	if got.Source != LLMSourceDeployment || got.BaseURL != envAll.BaseURL {
		t.Fatalf("cleared workspace config did not fall back: %+v", got)
	}
}

// TestResolveWorkspaceLLMSealedKeyRoundTrip: the stored key decrypts to the
// exact plaintext that was sealed.
func TestResolveWorkspaceLLMSealedKeyRoundTrip(t *testing.T) {
	box := testSecretBox(t)
	wsID := retroFixture(t, "")
	saveWSLLM(t, wsID, "https://api.openai.com/v1", "", sealWSKey(t, box, "sk-test-abcdef123456"), "3456")

	got := resolveTest(t, box, wsID, DeploymentLLM{})
	if got.APIKey != "sk-test-abcdef123456" {
		t.Fatalf("roundtrip key = %q, want sealed plaintext", got.APIKey)
	}
	if !got.Effective || got.KeySource != LLMSourceWorkspace {
		t.Fatalf("workspace key must be effective: %+v", got)
	}
}

// TestResolveWorkspaceLLMBoxNilWithStoredKey: a deployment that lost its
// secret box must fail closed — the workspace config is unusable and the
// resolution names the reason instead of silently falling back to env keys.
func TestResolveWorkspaceLLMBoxNilWithStoredKey(t *testing.T) {
	box := testSecretBox(t)
	wsID := retroFixture(t, "")
	saveWSLLM(t, wsID, "https://ws.example.com/v1", "", sealWSKey(t, box, "ws-key-0000"), "0000")

	got := resolveTest(t, nil, wsID, envAll)
	if got.Effective {
		t.Fatalf("stored key without a box must not be effective: %+v", got)
	}
	if got.Issue == "" {
		t.Fatal("box-missing resolution must name the issue")
	}
}

// TestResolveWorkspaceLLMTamperedSealed: a tampered ciphertext fails the GCM
// authentication check and names the issue — never decrypts to garbage that
// would be sent upstream.
func TestResolveWorkspaceLLMTamperedSealed(t *testing.T) {
	box := testSecretBox(t)
	wsID := retroFixture(t, "")
	sealed := sealWSKey(t, box, "ws-key-0000")
	sealed[len(sealed)-1] ^= 0xFF
	saveWSLLM(t, wsID, "", "", sealed, "0000")

	got := resolveTest(t, box, wsID, envAll)
	if got.Effective {
		t.Fatalf("tampered key must not be effective: %+v", got)
	}
	if got.Issue == "" {
		t.Fatal("tampered ciphertext must name the issue")
	}
}

// TestResolveWorkspaceLLMEnvOnlySemantics: the effective predicate matches
// llm.New exactly — a key alone or a base URL alone is enough (keyless local
// gateways are a supported shape); nothing at all is not.
func TestResolveWorkspaceLLMEnvOnlySemantics(t *testing.T) {
	box := testSecretBox(t)
	wsID := retroFixture(t, "")

	cases := []struct {
		name   string
		env    DeploymentLLM
		effect bool
	}{
		{"key only", DeploymentLLM{APIKey: "k1"}, true},
		{"base only", DeploymentLLM{BaseURL: "http://localhost:8080/v1"}, true},
		{"nothing", DeploymentLLM{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTest(t, box, wsID, tc.env)
			if got.Effective != tc.effect {
				t.Fatalf("effective = %v, want %v: %+v", got.Effective, tc.effect, got)
			}
			if !tc.effect && got.Source != LLMSourceNone {
				t.Fatalf("source = %q, want %q", got.Source, LLMSourceNone)
			}
		})
	}
}

// errLLM fails every call with a fixed error, like an upstream 401 would.
type errLLM struct{ err error }

func (e *errLLM) GenerateJSON(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64, maxCompletionTokens int64) (string, error) {
	return "", e.err
}
func (e *errLLM) Enabled() bool { return true }

// runDetail reads the newest run record's detail JSON.
func runDetail(t *testing.T, wsID string) map[string]any {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT detail FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("detail not JSON: %v", err)
	}
	return out
}

// TestRunWorkspaceLLMDisabledGuidesToUI: the failure the user sees points at
// the product's own LLM config surface, never at environment variables, and
// the run detail carries the machine-readable llm_configured marker the UI
// renders its 配置 LLM entry from.
func TestRunWorkspaceLLMDisabledGuidesToUI(t *testing.T) {
	wsID := retroFixture(t, "# 规范\n")
	retroIssue(t, wsID, "复盘测试 G", "讨论内容庚")

	runner := newTestRunner(nil)
	stats, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("disabled LLM run must not error: %v", err)
	}
	_, errMsg := runRecord(t, wsID)
	if !strings.Contains(errMsg, "LLM") || !strings.Contains(errMsg, "配置") {
		t.Fatalf("disabled-LLM error must guide to the config UI, got %q", errMsg)
	}
	if strings.Contains(errMsg, "MULTICA_") {
		t.Fatalf("disabled-LLM error must not name env vars, got %q", errMsg)
	}
	detail := runDetail(t, wsID)
	if v, ok := detail["llm_configured"].(bool); !ok || v {
		t.Fatalf("detail.llm_configured must be false, got %v", detail["llm_configured"])
	}
	_ = stats
}

// TestRunWorkspaceLLMErrorRedactsKey: an upstream failure that echoes the
// API key back must not store it in the run record.
func TestRunWorkspaceLLMErrorRedactsKey(t *testing.T) {
	wsID := retroFixture(t, "# 规范\n")
	retroIssue(t, wsID, "复盘测试 辛", "讨论内容辛")

	secret := "sk-live-DO-NOT-STORE"
	runner := newTestRunner(nil)
	runner.LLM = &errLLM{err: errors.New("upstream 401: bad key " + secret + " rejected")}
	runner.Redact = []string{secret}

	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("run must not error: %v", err)
	}
	_, errMsg := runRecord(t, wsID)
	if strings.Contains(errMsg, secret) {
		t.Fatalf("run record stored the API key: %q", errMsg)
	}
	if !strings.Contains(errMsg, "[redacted]") {
		t.Fatalf("run record must redact the key in place, got %q", errMsg)
	}
}
