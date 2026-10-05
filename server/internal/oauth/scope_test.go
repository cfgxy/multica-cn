package oauth

import (
	"errors"
	"testing"
)

func TestNormalizeRequestScopeEmptyMeansLegacyFull(t *testing.T) {
	got, err := NormalizeRequestScope("")
	if err != nil {
		t.Fatalf("empty scope: %v", err)
	}
	if got != ScopeMCP {
		t.Fatalf("empty scope normalized to %q, want legacy %q", got, ScopeMCP)
	}
}

func TestNormalizeRequestScopeTiers(t *testing.T) {
	cases := map[string]string{
		"mcp:read":               ScopeRead,
		"  mcp:write  ":          ScopeWrite,
		"mcp:read mcp:run":       ScopeRead + " " + ScopeRun,
		"mcp:run mcp:read":       ScopeRun + " " + ScopeRead,
		"mcp:read mcp:read":      ScopeRead,
		"mcp":                    ScopeMCP,
		"mcp mcp:read":           ScopeMCP,
		"mcp:read mcp mcp:write": ScopeMCP,
	}
	for in, want := range cases {
		got, err := NormalizeRequestScope(in)
		if err != nil {
			t.Fatalf("NormalizeRequestScope(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("NormalizeRequestScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRequestScopeRejectsUnknown(t *testing.T) {
	// A consent screen that shows less than will be granted is a consent
	// defect, so an unknown value kills the request instead of being dropped.
	for _, in := range []string{"mcp:admin", "mcp:read nope", "read"} {
		if _, err := NormalizeRequestScope(in); !errors.Is(err, ErrUnknownScope) {
			t.Fatalf("NormalizeRequestScope(%q) error = %v, want ErrUnknownScope", in, err)
		}
	}
}

func TestScopeCovers(t *testing.T) {
	cases := []struct {
		granted, required string
		want              bool
	}{
		{ScopeMCP, ScopeRead, true},
		{ScopeMCP, ScopeRun, true},
		{ScopeRead, ScopeRead, true},
		{ScopeRead + " " + ScopeWrite, ScopeWrite, true},
		{ScopeRead, ScopeWrite, false},
		{ScopeWrite + " " + ScopeRun, ScopeRead, false},
		// An empty granted scope covers nothing: tokens are always minted
		// with a scope, so empty means a hand-forged claim.
		{"", ScopeRead, false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := ScopeCovers(c.granted, c.required); got != c.want {
			t.Fatalf("ScopeCovers(%q, %q) = %v, want %v", c.granted, c.required, got, c.want)
		}
	}
}

func TestScopeTiersOfExpandsLegacy(t *testing.T) {
	tiers := ScopeTiersOf(ScopeMCP)
	if len(tiers) != 3 {
		t.Fatalf("ScopeTiersOf(mcp) = %v, want the three tiers", tiers)
	}
	if got := ScopeTiersOf(ScopeRead + " " + ScopeRun); len(got) != 2 || got[0] != ScopeRead || got[1] != ScopeRun {
		t.Fatalf("ScopeTiersOf(explicit) = %v, want pass-through in order", got)
	}
}
