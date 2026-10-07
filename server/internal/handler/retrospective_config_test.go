package handler

// Tests for the retrospective LLM config surface (RUYI-552): the UI saves a
// workspace LLM config (provider base URL / model / API key), the key is
// sealed with the server secret box and never echoed back, the status view
// exposes only masked hints and per-field sources, and an unconfigured run
// returns a machine-readable llm_not_configured code the UI renders its
// 配置 LLM entry from.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

const envTestKey = "env-key-9999"

func handlerSecretBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	return box
}

// useRetrospectiveLLMFixture swaps the testHandler's credential box and
// deployment defaults for the duration of one test.
func useRetrospectiveLLMFixture(t *testing.T, box *secretbox.Box, env retrospective.DeploymentLLM) {
	t.Helper()
	oldBox, oldEnv := testHandler.RuntimeCredentialBox, testHandler.DeploymentLLM
	testHandler.RuntimeCredentialBox, testHandler.DeploymentLLM = box, env
	t.Cleanup(func() {
		testHandler.RuntimeCredentialBox, testHandler.DeploymentLLM = oldBox, oldEnv
	})
}

func retroConfigCall(t *testing.T, method string, body any, wsID, userID string) (int, string) {
	t.Helper()
	var h http.HandlerFunc
	switch method {
	case http.MethodGet:
		h = testHandler.GetRetrospectiveConfig
	case http.MethodPut:
		h = testHandler.UpdateRetrospectiveConfig
	case http.MethodPost:
		h = testHandler.TriggerRetrospectiveRun
	}
	return legislationCall(t, h, legislationReq(userID, wsID, method, "/api/retrospective/config", body))
}

type retroLLMStatus struct {
	Stored struct {
		BaseURL    string `json:"base_url"`
		Model      string `json:"model"`
		APIKeySet  bool   `json:"api_key_set"`
		APIKeyHint string `json:"api_key_hint"`
	} `json:"stored"`
	Effective struct {
		Source          string `json:"source"`
		BaseURL         string `json:"base_url"`
		BaseURLSource   string `json:"base_url_source"`
		Model           string `json:"model"`
		ModelSource     string `json:"model_source"`
		APIKeyHint      string `json:"api_key_hint"`
		APIKeySource    string `json:"api_key_source"`
		Issue           string `json:"issue"`
	} `json:"effective"`
}

func getRetroConfig(t *testing.T, wsID, ownerID string) retroLLMStatus {
	t.Helper()
	code, body := retroConfigCall(t, http.MethodGet, nil, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("GET config: expected 200, got %d: %s", code, body)
	}
	var cfg struct {
		Enabled          bool           `json:"enabled"`
		IncludeInReview  bool           `json:"include_in_review"`
		WindowDays       int            `json:"window_days"`
		LLM              retroLLMStatus `json:"llm"`
	}
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("GET config body: %v", err)
	}
	return cfg.LLM
}

// TestRetrospectiveLLMConfigSaveStatusAndNoPlaintext: saving a workspace LLM
// config round-trips through the status view with masked hints only, and the
// plaintext key never appears in any response.
func TestRetrospectiveLLMConfigSaveStatusAndNoPlaintext(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, handlerSecretBox(t), retrospective.DeploymentLLM{
		APIKey: envTestKey, BaseURL: "https://env.example.com/v1", DefaultModel: "env-model",
	})

	body := map[string]any{
		"enabled":     true,
		"window_days": 3,
		"llm": map[string]any{
			"base_url": "https://ws.example.com/v1",
			"model":    "ws-model-x",
			"api_key":  "sk-save-4321",
		},
	}
	code, resp := retroConfigCall(t, http.MethodPut, body, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("PUT config: expected 200, got %d: %s", code, resp)
	}
	if strings.Contains(resp, "sk-save-4321") {
		t.Fatal("PUT response echoed the API key")
	}

	llm := getRetroConfig(t, wsID, ownerID)
	if !llm.Stored.APIKeySet || llm.Stored.APIKeyHint != "4321" {
		t.Fatalf("stored key status wrong: %+v", llm.Stored)
	}
	if llm.Stored.BaseURL != "https://ws.example.com/v1" || llm.Stored.Model != "ws-model-x" {
		t.Fatalf("stored fields wrong: %+v", llm.Stored)
	}
	if llm.Effective.Source != "workspace" || llm.Effective.APIKeySource != "workspace" {
		t.Fatalf("effective sources wrong: %+v", llm.Effective)
	}
	if llm.Effective.APIKeyHint != "4321" {
		t.Fatalf("effective hint wrong: %+v", llm.Effective)
	}

	// The deployment defaults are visible as fallbacks while no field is
	// saved — and the env key must never appear in plaintext.
	ws2, owner2, _ := legislationFixture(t)
	llm2 := getRetroConfig(t, ws2, owner2)
	if llm2.Effective.Source != "deployment" || llm2.Effective.APIKeySource != "deployment" {
		t.Fatalf("env fallback sources wrong: %+v", llm2.Effective)
	}
	if llm2.Effective.APIKeyHint != "9999" {
		t.Fatalf("env key hint wrong: %+v", llm2.Effective)
	}
	if llm2.Effective.BaseURL != "https://env.example.com/v1" || llm2.Effective.Model != "env-model" {
		t.Fatalf("env fallback display wrong: %+v", llm2.Effective)
	}
}

// TestRetrospectiveLLMConfigBaseURLSanitized: a deployment base URL with
// embedded credentials or a query string is stripped before display.
func TestRetrospectiveLLMConfigBaseURLSanitized(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, handlerSecretBox(t), retrospective.DeploymentLLM{
		APIKey: envTestKey, BaseURL: "https://u:p@env.example.com/v1?token=zzz",
	})
	llm := getRetroConfig(t, wsID, ownerID)
	if llm.Effective.BaseURL != "https://env.example.com/v1" {
		t.Fatalf("sanitized base URL = %q, want credentials and query stripped", llm.Effective.BaseURL)
	}
}

// TestRetrospectiveLLMConfigValidation: bad shapes are refused with 400.
func TestRetrospectiveLLMConfigValidation(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, handlerSecretBox(t), retrospective.DeploymentLLM{APIKey: envTestKey})

	cases := []struct {
		name string
		llm  map[string]any
	}{
		{"bad scheme", map[string]any{"base_url": "ftp://ws.example.com/v1"}},
		{"not a url", map[string]any{"base_url": "not a url"}},
		{"userinfo", map[string]any{"base_url": "https://user:pass@ws.example.com/v1"}},
		{"query string", map[string]any{"base_url": "https://ws.example.com/v1?x=1"}},
		{"long key", map[string]any{"api_key": strings.Repeat("k", 8193)}},
		{"long model", map[string]any{"model": strings.Repeat("m", 257)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := retroConfigCall(t, http.MethodPut,
				map[string]any{"llm": tc.llm}, wsID, ownerID)
			if code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", code, body)
			}
		})
	}
}

// TestRetrospectiveLLMConfigBoxNilFailsClosed: a deployment without the
// secret box refuses to store a new key (503), while non-secret fields still
// save.
func TestRetrospectiveLLMConfigBoxNilFailsClosed(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, nil, retrospective.DeploymentLLM{})

	code, body := retroConfigCall(t, http.MethodPut,
		map[string]any{"llm": map[string]any{"api_key": "sk-new"}}, wsID, ownerID)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", code, body)
	}

	code, body = retroConfigCall(t, http.MethodPut,
		map[string]any{"llm": map[string]any{"base_url": "https://ws.example.com/v1"}}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("non-secret save without box: expected 200, got %d: %s", code, body)
	}
}

// TestRetrospectiveLLMConfigClearKey: api_key "" clears the stored key; the
// resolution falls back to the deployment default.
func TestRetrospectiveLLMConfigClearKey(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, handlerSecretBox(t), retrospective.DeploymentLLM{
		APIKey: envTestKey, BaseURL: "https://env.example.com/v1", DefaultModel: "env-model",
	})

	code, _ := retroConfigCall(t, http.MethodPut,
		map[string]any{"llm": map[string]any{"api_key": "sk-clear-1111"}}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("save: expected 200")
	}
	code, body := retroConfigCall(t, http.MethodPut,
		map[string]any{"llm": map[string]any{"api_key": ""}}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %s", code, body)
	}
	llm := getRetroConfig(t, wsID, ownerID)
	if llm.Stored.APIKeySet || llm.Stored.APIKeyHint != "" {
		t.Fatalf("key not cleared: %+v", llm.Stored)
	}
	if llm.Effective.APIKeySource != "deployment" || llm.Effective.APIKeyHint != "9999" {
		t.Fatalf("clear did not fall back to env: %+v", llm.Effective)
	}
}

// TestTriggerRetrospectiveRunLLMNotConfiguredCode: with no config anywhere
// the manual trigger returns 409 carrying the llm_not_configured code.
func TestTriggerRetrospectiveRunLLMNotConfiguredCode(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveLLMFixture(t, handlerSecretBox(t), retrospective.DeploymentLLM{})

	code, body := retroConfigCall(t, http.MethodPost, nil, wsID, ownerID)
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, body)
	}
	var resp struct{ Code string `json:"code"` }
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Code != "llm_not_configured" {
		t.Fatalf("409 body must carry code llm_not_configured, got %s (%v)", body, err)
	}
}
