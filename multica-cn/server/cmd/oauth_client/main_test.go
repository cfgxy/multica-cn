package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/oauth"
)

func TestValidateRedirectURIsAcceptsHTTPSAndLoopback(t *testing.T) {
	for _, uri := range []string{
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"http://localhost:3000/callback",
		"http://127.0.0.1:3000/callback",
	} {
		if err := oauth.ValidateRedirectURIs([]string{uri}); err != nil {
			t.Errorf("ValidateRedirectURIs(%q) = %v, want nil", uri, err)
		}
	}
}

func TestValidateRedirectURIsRejectsUnusableEntries(t *testing.T) {
	cases := map[string][]string{
		"empty list":       {},
		"relative":         {"/callback"},
		"plain http":       {"http://example.com/callback"},
		"carries fragment": {"https://example.com/callback#done"},
	}
	for name, uris := range cases {
		t.Run(name, func(t *testing.T) {
			if err := oauth.ValidateRedirectURIs(uris); err == nil {
				t.Fatalf("ValidateRedirectURIs(%v) = nil, want an error", uris)
			}
		})
	}
}

func TestNewClientSecretIsUniqueAndFullLength(t *testing.T) {
	first, err := oauth.NewClientSecret()
	if err != nil {
		t.Fatalf("NewClientSecret() error = %v", err)
	}
	second, err := oauth.NewClientSecret()
	if err != nil {
		t.Fatalf("NewClientSecret() error = %v", err)
	}
	if len(first) != 64 {
		t.Errorf("len = %d, want 64 hex chars", len(first))
	}
	if first == second {
		t.Error("two calls returned the same credential")
	}
}
