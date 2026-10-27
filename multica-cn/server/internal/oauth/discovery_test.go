package oauth

import (
	"encoding/json"
	"testing"
)

const testSiteRoot = "https://multica.example.com"

// The document a client fetches first (RFC 9728). Every field it needs to find
// the authorization server is asserted, because a missing one leaves ChatGPT
// with nowhere to send the authorization request.
func TestProtectedResourceMetadataFields(t *testing.T) {
	doc := ProtectedResourceMetadata(testSiteRoot)

	if got := doc["resource"]; got != testSiteRoot+"/api/mcp" {
		t.Fatalf("resource = %v, want the absolute MCP endpoint URL", got)
	}
	assertStringSlice(t, doc, "authorization_servers", []string{testSiteRoot})
	assertStringSlice(t, doc, "scopes_supported", []string{ScopeMCP})
	assertStringSlice(t, doc, "bearer_methods_supported", []string{"header"})
}

func TestProtectedResourceMetadataNormalizesTrailingSlash(t *testing.T) {
	// MULTICA_APP_URL is operator-set and may carry a trailing slash; a doubled
	// slash in `resource` would not match the audience the token carries.
	doc := ProtectedResourceMetadata(testSiteRoot + "/")
	if got := doc["resource"]; got != testSiteRoot+"/api/mcp" {
		t.Fatalf("resource = %v, want no doubled slash", got)
	}
}

func TestAuthorizationServerMetadataFields(t *testing.T) {
	doc := AuthorizationServerMetadata(testSiteRoot)

	for field, want := range map[string]string{
		"issuer":                 testSiteRoot,
		"authorization_endpoint": testSiteRoot + "/auth/oauth/authorize",
		"token_endpoint":         testSiteRoot + "/auth/oauth/token",
		"jwks_uri":               testSiteRoot + "/.well-known/jwks.json",
	} {
		if got := doc[field]; got != want {
			t.Fatalf("%s = %v, want %q", field, got, want)
		}
	}
	assertStringSlice(t, doc, "response_types_supported", []string{"code"})
	assertStringSlice(t, doc, "grant_types_supported", []string{"authorization_code"})
	assertStringSlice(t, doc, "scopes_supported", []string{ScopeMCP})
	assertStringSlice(t, doc, "token_endpoint_auth_methods_supported", []string{"client_secret_post", "client_secret_basic"})
}

// The single field the whole ChatGPT integration hinges on: a client that does
// not see "S256" here treats the server as not supporting PKCE and refuses to
// connect.
func TestAuthorizationServerMetadataAdvertisesS256(t *testing.T) {
	doc := AuthorizationServerMetadata(testSiteRoot)
	methods, ok := doc["code_challenge_methods_supported"].([]string)
	if !ok {
		t.Fatalf("code_challenge_methods_supported has type %T, want []string", doc["code_challenge_methods_supported"])
	}
	found := false
	for _, method := range methods {
		if method == "S256" {
			found = true
		}
		if method == "plain" {
			t.Fatalf("code_challenge_methods_supported advertises %q, which this server does not accept", method)
		}
	}
	if !found {
		t.Fatalf("code_challenge_methods_supported = %v, want it to contain \"S256\"", methods)
	}
}

// Both documents are served as JSON, so every value must survive the encoder —
// a map holding an unserializable value would 500 at request time, not here.
func TestDiscoveryDocumentsAreJSONSerializable(t *testing.T) {
	for name, doc := range map[string]map[string]any{
		"protected-resource":   ProtectedResourceMetadata(testSiteRoot),
		"authorization-server": AuthorizationServerMetadata(testSiteRoot),
	} {
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal %s document: %v", name, err)
		}
		var round map[string]any
		if err := json.Unmarshal(encoded, &round); err != nil {
			t.Fatalf("unmarshal %s document: %v", name, err)
		}
	}
}

func assertStringSlice(t *testing.T, doc map[string]any, field string, want []string) {
	t.Helper()
	got, ok := doc[field].([]string)
	if !ok {
		t.Fatalf("%s has type %T, want []string", field, doc[field])
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
	}
}
