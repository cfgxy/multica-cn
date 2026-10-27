package oauth

import "strings"

// MCPResourcePath is the MCP endpoint the discovery documents describe.
//
// It is `/api/mcp`, not `/mcp`: the Next.js proxy matcher already covers
// `/api/:path*`, so routing the MCP endpoint under that prefix needs one rewrite
// rule instead of a rule plus a matcher change
// (docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §3.3).
const MCPResourcePath = "/api/mcp"

// ProtectedResourceMetadata builds the RFC 9728 protected-resource document.
//
// `resource` is the absolute URL of the MCP endpoint and `authorization_servers`
// names this same deployment: the authorization server and the resource server
// are one Go process behind one origin. The absolute form matters — a client
// reads this document to decide where to send the authorization request, and a
// relative value would resolve against whatever origin it happened to fetch
// from.
func ProtectedResourceMetadata(siteRoot string) map[string]any {
	root := normalizeRoot(siteRoot)
	return map[string]any{
		"resource":                 root + MCPResourcePath,
		"authorization_servers":    []string{root},
		"scopes_supported":         []string{ScopeMCP},
		"bearer_methods_supported": []string{"header"},
	}
}

// AuthorizationServerMetadata builds the RFC 8414 authorization-server document.
//
// `code_challenge_methods_supported` containing "S256" is not cosmetic: OpenAI
// documents an MCP server whose metadata omits it as unsupported, so this field
// is the one the whole ChatGPT integration hinges on.
func AuthorizationServerMetadata(siteRoot string) map[string]any {
	root := normalizeRoot(siteRoot)
	return map[string]any{
		"issuer":                                root,
		"authorization_endpoint":                root + "/auth/oauth/authorize",
		"token_endpoint":                        root + "/auth/oauth/token",
		"jwks_uri":                              root + "/.well-known/jwks.json",
		"scopes_supported":                      []string{ScopeMCP},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{MethodS256},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
	}
}

func normalizeRoot(siteRoot string) string {
	return strings.TrimRight(strings.TrimSpace(siteRoot), "/")
}
