package sub2api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelDiscoveryUsesModelCredentialAndProtocolAuthentication(t *testing.T) {
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/models" {
			t.Fatal("unexpected discovery path")
		}
		if r.Header.Get("x-api-key") == "fixture-model" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") != "2023-06-01" {
				t.Error("invalid Anthropic authentication")
			}
			if r.URL.Query().Get("after_id") == "" {
				fmt.Fprint(w, `{"data":[{"id":"claude-one"}],"has_more":true,"last_id":"claude-one"}`)
			} else {
				fmt.Fprint(w, `{"data":[{"id":"shared"}],"has_more":false}`)
			}
		} else {
			if r.Header.Get("Authorization") != "Bearer fixture-model" {
				t.Error("directory credential used for models")
			}
			fmt.Fprint(w, `{"data":[{"id":"shared","display_name":"Shared"},{"id":"gpt-one"}]}`)
		}
	}))
	defer s.Close()
	key := Resolved{APIKeyToken: "fixture-model", BaseURL: s.URL, Endpoints: []Endpoint{{APIType: "anthropic-messages"}, {APIType: "openai-responses"}, {APIType: "openai-chat-completions"}}}
	models, err := (&Client{HTTP: s.Client()}).DiscoverModels(t.Context(), key)
	if err != nil || len(models) != 3 || calls != 3 || len(models[1].APITypes) != 3 {
		t.Fatalf("discovery merge/pagination failed: %v", err)
	}
}

func TestModelDiscoveryRejectsInvalidListsAndClassifiesErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		code   string
	}{
		{`{"data":[]}`, 200, "model_discovery_empty"},
		{`{}`, 200, "model_discovery_invalid"},
		{`{"data":[{"id":"a"},{"id":"a"}]}`, 200, "model_discovery_invalid"},
		{`{"data":[{"id":"a"}],"has_more":true}`, 200, "model_discovery_invalid"},
		{`secret-response-body`, 401, "model_auth_failed"},
		{`secret-response-body`, 429, "model_rate_limited"},
		{`secret-response-body`, 503, "model_discovery_failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			_, err := (&Client{HTTP: s.Client()}).DiscoverModels(t.Context(), Resolved{APIKeyToken: "model-secret", BaseURL: s.URL, Endpoints: []Endpoint{{APIType: "openai-responses"}}})
			if Code(err) != tc.code {
				t.Fatalf("unexpected classified error: %v", err)
			}
		})
	}
}
