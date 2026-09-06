package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// The catalog is embedded at build time, so a malformed entry is a build
// defect. Loading it in a test is what turns that defect into a failing build
// rather than a 500 the first admin to open the marketplace discovers.
func TestMarketplaceCatalog_LoadsAndValidates(t *testing.T) {
	items, err := MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("catalog is empty; the marketplace would list nothing")
	}

	var skills, mcps int
	for _, item := range items {
		switch item.Kind {
		case MarketplaceKindSkill:
			skills++
		case MarketplaceKindMcp:
			mcps++
		default:
			t.Fatalf("item %q has unknown kind %q", item.Key, item.Kind)
		}
		if item.Summary == "" {
			t.Errorf("item %q has no summary; the listing card would be blank", item.Key)
		}
	}
	// The whole point of the unified marketplace is that both extension kinds
	// are reachable from it.
	if skills == 0 {
		t.Error("catalog carries no skill entries")
	}
	if mcps == 0 {
		t.Error("catalog carries no MCP entries")
	}
}

// Sorting is what keeps the listing stable across processes; without it the
// order follows map/dir iteration and the UI reshuffles between requests.
func TestMarketplaceCatalog_SortedByKindThenName(t *testing.T) {
	items, err := MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		if prev.Kind > cur.Kind || (prev.Kind == cur.Kind && prev.Name > cur.Name) {
			t.Fatalf("catalog is unsorted at %d: %s/%s before %s/%s",
				i, prev.Kind, prev.Name, cur.Kind, cur.Name)
		}
	}
}

// No catalog entry may ship a credential. A curated template carries
// placeholders; the real value only ever arrives with an install request.
func TestMarketplaceCatalog_CarriesNoSecrets(t *testing.T) {
	items, err := MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, item := range items {
		if len(item.ConfigTemplate) == 0 {
			continue
		}
		declared := map[string]struct{}{}
		for _, placeholder := range item.Placeholders {
			declared[placeholder.Key] = struct{}{}
		}
		// Every substitution point in the template must correspond to a
		// declared placeholder, otherwise an install can never fill it and the
		// stored entry keeps a literal `${...}`.
		for _, token := range placeholderTokens(string(item.ConfigTemplate)) {
			if _, ok := declared[token]; !ok {
				t.Errorf("item %q template references undeclared placeholder %q", item.Key, token)
			}
		}
		// Conversely, a declared secret that the template never substitutes
		// would silently drop the value an admin typed.
		for key := range declared {
			if !strings.Contains(string(item.ConfigTemplate), "${"+key+"}") {
				t.Errorf("item %q declares placeholder %q the template never uses", item.Key, key)
			}
		}
	}
}

func placeholderTokens(s string) []string {
	var out []string
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			return out
		}
		s = s[start+2:]
		end := strings.Index(s, "}")
		if end < 0 {
			return out
		}
		out = append(out, s[:end])
		s = s[end+1:]
	}
}

func TestFindMarketplaceItem(t *testing.T) {
	items, err := MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	want := items[0]
	got, ok := FindMarketplaceItem(want.Key)
	if !ok {
		t.Fatalf("catalog entry %q not found by key", want.Key)
	}
	if got.Key != want.Key {
		t.Fatalf("found %q, want %q", got.Key, want.Key)
	}
	if _, ok := FindMarketplaceItem("mcp:does-not-exist"); ok {
		t.Fatal("unknown key resolved to an item")
	}
}

func TestValidateMarketplaceItem_Rejects(t *testing.T) {
	tests := []struct {
		name string
		item MarketplaceItem
		want string
	}{
		{
			name: "missing key",
			item: MarketplaceItem{Kind: MarketplaceKindSkill, Name: "x", SourceURL: "https://example.com"},
			want: "missing a key",
		},
		{
			name: "missing name",
			item: MarketplaceItem{Key: "k", Kind: MarketplaceKindSkill, SourceURL: "https://example.com"},
			want: "missing a name",
		},
		{
			name: "unknown kind",
			item: MarketplaceItem{Key: "k", Kind: "plugin", Name: "x"},
			want: "unknown kind",
		},
		{
			name: "skill without source",
			item: MarketplaceItem{Key: "k", Kind: MarketplaceKindSkill, Name: "x"},
			want: "missing source_url",
		},
		{
			// A skill install goes through the skill import path, which has no
			// idea what an MCP entry is; carrying one means the catalog author
			// picked the wrong kind.
			name: "skill carrying an mcp template",
			item: MarketplaceItem{
				Key: "k", Kind: MarketplaceKindSkill, Name: "x",
				SourceURL:      "https://example.com",
				ConfigTemplate: json.RawMessage(`{"command":"npx"}`),
			},
			want: "must not carry an MCP config_template",
		},
		{
			name: "mcp without template",
			item: MarketplaceItem{Key: "k", Kind: MarketplaceKindMcp, Name: "x"},
			want: "missing config_template",
		},
		{
			name: "mcp with non-object template",
			item: MarketplaceItem{
				Key: "k", Kind: MarketplaceKindMcp, Name: "x",
				ConfigTemplate: json.RawMessage(`["npx"]`),
			},
			want: "not a JSON object",
		},
		{
			name: "mcp with empty template",
			item: MarketplaceItem{
				Key: "k", Kind: MarketplaceKindMcp, Name: "x",
				ConfigTemplate: json.RawMessage(`{}`),
			},
			want: "empty config_template",
		},
		{
			name: "mcp carrying a skill source",
			item: MarketplaceItem{
				Key: "k", Kind: MarketplaceKindMcp, Name: "x",
				ConfigTemplate: json.RawMessage(`{"command":"npx"}`),
				SourceURL:      "https://example.com",
			},
			want: "must not carry a skill source_url",
		},
		{
			name: "placeholder without a key",
			item: MarketplaceItem{
				Key: "k", Kind: MarketplaceKindMcp, Name: "x",
				ConfigTemplate: json.RawMessage(`{"command":"npx"}`),
				Placeholders:   []MarketplacePlaceholder{{Label: "Token"}},
			},
			want: "placeholder without a key",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMarketplaceItem(tc.item)
			if err == nil {
				t.Fatal("expected the item to be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func githubItem() MarketplaceItem {
	return MarketplaceItem{
		Key:  "mcp:test/github",
		Kind: MarketplaceKindMcp,
		Name: "github",
		ConfigTemplate: json.RawMessage(
			`{"command":"npx","args":["-y","server-github"],"env":{"TOKEN":"${github_token}"}}`),
		Placeholders: []MarketplacePlaceholder{
			{Key: "github_token", Secret: true, Required: true},
		},
	}
}

func TestRenderMarketplaceMcpConfig_SubstitutesValues(t *testing.T) {
	got, err := RenderMarketplaceMcpConfig(githubItem(), map[string]string{"github_token": "ghp_example"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var entry struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(got, &entry); err != nil {
		t.Fatalf("rendered entry is not valid JSON: %v", err)
	}
	if entry.Env["TOKEN"] != "ghp_example" {
		t.Fatalf("TOKEN = %q, want the supplied value", entry.Env["TOKEN"])
	}
}

// A value carrying a quote must land inside its JSON string rather than
// terminating it. Without encoding, `"` would let a supplied value append
// arbitrary keys to the stored MCP entry — including a `command` the admin
// never approved.
func TestRenderMarketplaceMcpConfig_EscapesInjectedValue(t *testing.T) {
	hostile := `x","command":"/bin/sh`
	got, err := RenderMarketplaceMcpConfig(githubItem(), map[string]string{"github_token": hostile})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var entry struct {
		Command string            `json:"command"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(got, &entry); err != nil {
		t.Fatalf("rendered entry is not valid JSON: %v", err)
	}
	if entry.Command != "npx" {
		t.Fatalf("command = %q; the supplied value escaped its string and rewrote the entry", entry.Command)
	}
	if entry.Env["TOKEN"] != hostile {
		t.Fatalf("TOKEN = %q, want the value stored verbatim", entry.Env["TOKEN"])
	}
}

func TestRenderMarketplaceMcpConfig_RequiresValues(t *testing.T) {
	if _, err := RenderMarketplaceMcpConfig(githubItem(), nil); err == nil {
		t.Fatal("expected a missing required value to be rejected")
	}
	if _, err := RenderMarketplaceMcpConfig(githubItem(), map[string]string{"github_token": "  "}); err == nil {
		t.Fatal("expected a blank required value to be rejected")
	}
}

// A typo in a secret's name must fail loudly. Ignoring it would store an entry
// that authenticates as nobody and fails at runtime, far from the install.
func TestRenderMarketplaceMcpConfig_RejectsUnknownValue(t *testing.T) {
	_, err := RenderMarketplaceMcpConfig(githubItem(), map[string]string{
		"github_token": "ghp_example",
		"githbu_token": "ghp_example",
	})
	if err == nil {
		t.Fatal("expected an unknown configuration key to be rejected")
	}
	if strings.Contains(err.Error(), "ghp_example") {
		t.Fatalf("error echoes the supplied secret: %q", err)
	}
}

// An optional placeholder the template still substitutes must not leave a
// literal `${...}` in the stored entry.
func TestRenderMarketplaceMcpConfig_OptionalPlaceholderClearsToken(t *testing.T) {
	item := githubItem()
	item.Placeholders = []MarketplacePlaceholder{{Key: "github_token"}}
	got, err := RenderMarketplaceMcpConfig(item, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(got), "${") {
		t.Fatalf("rendered entry keeps an unfilled placeholder: %s", got)
	}
}

func TestRenderMarketplaceMcpConfig_RejectsNonMcpItem(t *testing.T) {
	item := MarketplaceItem{Key: "skill:x", Kind: MarketplaceKindSkill, Name: "x"}
	if _, err := RenderMarketplaceMcpConfig(item, nil); err == nil {
		t.Fatal("expected a skill item to be rejected")
	}
}

// The error a malformed render produces must never quote the document: the
// document is the place the token lives.
func TestRenderMarketplaceMcpConfig_ErrorNeverEchoesEntry(t *testing.T) {
	item := githubItem()
	item.ConfigTemplate = json.RawMessage(`{"env":{"TOKEN":"${github_token}"}`) // missing brace
	_, err := RenderMarketplaceMcpConfig(item, map[string]string{"github_token": "ghp_example"})
	if err == nil {
		t.Fatal("expected a malformed template to be rejected")
	}
	if strings.Contains(err.Error(), "ghp_example") {
		t.Fatalf("error echoes the supplied secret: %q", err)
	}
}
