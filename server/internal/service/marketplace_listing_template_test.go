package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// mcpDraft builds a minimal MCP listing draft whose only interesting part is
// the template under test.
func mcpDraft(template string, placeholders ...MarketplacePlaceholder) MarketplaceItem {
	return MarketplaceItem{
		Kind:           MarketplaceKindMcp,
		Name:           "sample",
		Summary:        "a sample listing",
		Categories:     []string{"development"},
		ConfigTemplate: json.RawMessage(template),
		Placeholders:   placeholders,
	}
}

// A template whose schema fields carry the wrong JSON type cannot produce a
// runnable MCP entry, so publishing must fail closed rather than hand every
// installing workspace an entry that only breaks at launch.
func TestValidateMarketplaceListingDraft_RejectsMalformedTemplateStructure(t *testing.T) {
	cases := []struct {
		name     string
		template string
	}{
		{"command is not a string", `{"type":"stdio","command":42}`},
		{"command is an empty string", `{"type":"stdio","command":""}`},
		{"command is only whitespace", `{"type":"stdio","command":"   "}`},
		{"command is null", `{"type":"stdio","command":null}`},
		{"url is not a string", `{"type":"http","url":true}`},
		{"url is an empty string", `{"type":"http","url":""}`},
		{"url is not http(s)", `{"type":"http","url":"file:///etc/passwd"}`},
		{"args is not an array", `{"type":"stdio","command":"npx","args":"-y srv"}`},
		{"args holds a non-string", `{"type":"stdio","command":"npx","args":["-y",7]}`},
		{"env is not an object", `{"type":"stdio","command":"npx","env":["A=1"]}`},
		{"env holds a non-string value", `{"type":"stdio","command":"npx","env":{"A":1}}`},
		{"headers is not an object", `{"type":"http","url":"https://example.invalid","headers":"A: 1"}`},
		{"headers holds a non-string value", `{"type":"http","url":"https://example.invalid","headers":{"A":null}}`},
		{"cwd is not a string", `{"type":"stdio","command":"npx","cwd":3}`},
		{"disabled is not a boolean", `{"type":"stdio","command":"npx","disabled":"yes"}`},
		{"timeout is not a number", `{"type":"stdio","command":"npx","timeout":"30s"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMarketplaceListingDraft(mcpDraft(tc.template)); err == nil {
				t.Fatalf("draft with %s was accepted, want rejection", tc.name)
			}
		})
	}
}

// The counterpart: a structurally sound template covering every schema field
// still publishes, so the new checks did not narrow the accepted protocol.
func TestValidateMarketplaceListingDraft_AcceptsWellFormedTemplates(t *testing.T) {
	cases := []struct {
		name         string
		template     string
		placeholders []MarketplacePlaceholder
	}{
		{
			name:         "stdio with args, env, cwd and switches",
			template:     `{"type":"stdio","command":"npx","args":["-y","srv","${root_path}"],"env":{"API_TOKEN":"${api_token}"},"cwd":"/srv","disabled":false,"timeout":30}`,
			placeholders: []MarketplacePlaceholder{{Key: "root_path"}, {Key: "api_token", Secret: true}},
		},
		{
			name:         "http with headers",
			template:     `{"type":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"${api_token}"}}`,
			placeholders: []MarketplacePlaceholder{{Key: "api_token", Secret: true}},
		},
		{
			name:     "sse without optional fields",
			template: `{"type":"sse","url":"https://example.invalid/sse"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMarketplaceListingDraft(mcpDraft(tc.template, tc.placeholders...)); err != nil {
				t.Fatalf("well-formed template was rejected: %v", err)
			}
		})
	}
}

// A placeholder key is publisher input reaching validation BEFORE the secret
// scanner sees the draft, so quoting it back would publish a token-shaped key
// in a 400 body. The rejection has to name the position instead.
func TestValidateMarketplaceListingDraft_NeverEchoesPlaceholderKeys(t *testing.T) {
	const tokenShaped = "sk-live-000000000000000000000000"

	cases := []struct {
		name  string
		draft MarketplaceItem
	}{
		{
			name: "key with an illegal character",
			draft: mcpDraft(`{"type":"stdio","command":"npx"}`,
				MarketplacePlaceholder{Key: tokenShaped + "!"}),
		},
		{
			name: "duplicate key",
			draft: mcpDraft(`{"type":"stdio","command":"npx","args":["${`+tokenShaped+`}"]}`,
				MarketplacePlaceholder{Key: tokenShaped}, MarketplacePlaceholder{Key: tokenShaped}),
		},
		{
			name: "key declared but never used",
			draft: mcpDraft(`{"type":"stdio","command":"npx"}`,
				MarketplacePlaceholder{Key: tokenShaped}),
		},
		{
			name: "label too long",
			draft: mcpDraft(`{"type":"stdio","command":"npx","args":["${`+tokenShaped+`}"]}`,
				MarketplacePlaceholder{Key: tokenShaped, Label: strings.Repeat("x", 101)}),
		},
		{
			name: "description too long",
			draft: mcpDraft(`{"type":"stdio","command":"npx","args":["${`+tokenShaped+`}"]}`,
				MarketplacePlaceholder{Key: tokenShaped, Description: strings.Repeat("x", 301)}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMarketplaceListingDraft(tc.draft)
			if err == nil {
				t.Fatalf("draft was accepted, want rejection")
			}
			if strings.Contains(err.Error(), tokenShaped) {
				t.Fatalf("error echoed the publisher-supplied placeholder key: %v", err)
			}
		})
	}
}

// The listing name reaches the shared shape check as a derived catalog key. It
// is publisher input too, so the same no-echo rule applies to every draft
// error, not only the placeholder ones.
func TestValidateMarketplaceListingDraft_NeverEchoesName(t *testing.T) {
	const tokenShaped = "sk-live-111111111111111111111111"

	draft := mcpDraft(`{"type":"stdio","command":"npx"}`)
	draft.Name = tokenShaped
	draft.SourceURL = "https://example.invalid/skill" // an MCP listing must not carry one
	err := ValidateMarketplaceListingDraft(draft)
	if err == nil {
		t.Fatalf("draft was accepted, want rejection")
	}
	if strings.Contains(err.Error(), tokenShaped) {
		t.Fatalf("error echoed the publisher-supplied name: %v", err)
	}
}
