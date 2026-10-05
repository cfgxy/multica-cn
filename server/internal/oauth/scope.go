package oauth

import "strings"

// The MCP scope model (RUYI-420). Three capability tiers plus the legacy
// value:
//
//   - mcp:read  — list/get/search calls
//   - mcp:write — create/update/comment/assign/bulk calls
//   - mcp:run   — dispatch_agent and run cancel/retry (things that start or
//     steer an agent run and consume the owner's quota)
//   - mcp       — the pre-420 value, kept as a full-access alias so tokens
//     already issued to connected clients keep working. New consents mint
//     explicit tiers; only an explicit request for "mcp" (or a token minted
//     before this change) produces it.
//
// Enforcement is server-side only, in one place: middleware.Auth maps the
// request's method + path onto the tier below (ScopeForRequest) and rejects
// with 403 when the token's scope does not cover it. tools/list stays
// unfiltered by design — a client sees the full catalogue and learns its
// actual authority from the 403s (ADR-001 §3.7).
const (
	ScopeRead  = "mcp:read"
	ScopeWrite = "mcp:write"
	ScopeRun   = "mcp:run"
)

// KnownScopes is every scope value the authorization server accepts in an
// authorize request. "mcp" stays accepted for back-compat with clients
// configured before the tiers existed. ScopeMCP itself is declared in
// signer.go next to the mint path that still defaults to it.
var KnownScopes = []string{ScopeMCP, ScopeRead, ScopeWrite, ScopeRun}

// scopeTiers is the tier set "mcp" expands to.
var scopeTiers = []string{ScopeRead, ScopeWrite, ScopeRun}

// NormalizeRequestScope validates the scope parameter of an authorize
// request and returns the canonical scope string to consent for and mint.
// An empty request means "mcp", matching the pre-tier default so existing
// client configurations that omit the parameter keep their behaviour.
// "mcp" swallows any tiers it appears with (it is their superset); unknown
// values are rejected rather than silently dropped — a consent screen that
// shows less than will be granted is a consent defect.
func NormalizeRequestScope(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ScopeMCP, nil
	}
	seen := make(map[string]bool)
	var picked []string
	for _, token := range strings.Fields(requested) {
		switch token {
		case ScopeMCP, ScopeRead, ScopeWrite, ScopeRun:
			if !seen[token] {
				seen[token] = true
				picked = append(picked, token)
			}
		default:
			return "", ErrUnknownScope
		}
	}
	if seen[ScopeMCP] {
		return ScopeMCP, nil
	}
	return strings.Join(picked, " "), nil
}

// ErrUnknownScope reports an authorize request carrying a scope value this
// server never granted. The whole request is rejected: granting a subset
// silently would mislead both the consent screen and the client.
var ErrUnknownScope = scopeError("unknown scope requested")

type scopeError string

func (e scopeError) Error() string { return string(e) }

// ScopeCovers reports whether the granted scope string includes the
// required tier. The legacy "mcp" covers every tier. An empty granted scope
// covers nothing — tokens are always minted with a scope, so an empty value
// means a hand-forged or corrupted claim.
func ScopeCovers(granted, required string) bool {
	if granted == ScopeMCP {
		return true
	}
	for _, tier := range strings.Fields(granted) {
		if tier == required {
			return true
		}
	}
	return false
}

// ScopeCoversAll reports whether granted includes every tier in required.
func ScopeCoversAll(granted string, required ...string) bool {
	for _, tier := range required {
		if !ScopeCovers(granted, tier) {
			return false
		}
	}
	return true
}

// ScopeIsFull reports whether the scope string is the legacy full-access
// value (equivalently: it covers all tiers).
func ScopeIsFull(scope string) bool { return scope == ScopeMCP }

// ScopeTiersOf expands a scope string into its concrete tiers. "mcp"
// expands to all three; explicit tiers pass through in listed order.
func ScopeTiersOf(scope string) []string {
	if scope == ScopeMCP || scope == "" {
		return append([]string(nil), scopeTiers...)
	}
	return strings.Fields(scope)
}

// IsKnownScope reports whether a single token is a scope this server grants.
func IsKnownScope(token string) bool {
	for _, known := range KnownScopes {
		if token == known {
			return true
		}
	}
	return false
}
