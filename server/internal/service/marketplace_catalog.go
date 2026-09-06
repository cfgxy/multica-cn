package service

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed marketplace_catalog/*.json
var marketplaceCatalogFS embed.FS

const marketplaceCatalogRoot = "marketplace_catalog"

// Marketplace item kinds. Plugins already have their own listing table
// (marketplace_plugin_listing); this catalog covers the dynamic extension
// capabilities that ship with the platform rather than being published by a
// workspace.
const (
	MarketplaceKindSkill = "skill"
	MarketplaceKindMcp   = "mcp"
)

// MarketplaceItem is one curated entry a workspace can install. It carries
// discovery metadata plus the input the existing install path needs, and
// nothing else: no credentials, no tokens, no runtime configuration that would
// make the catalog a second copy of a workspace's secrets.
type MarketplaceItem struct {
	// Key is the stable catalog identity, unique across every kind.
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Publisher   string   `json:"publisher"`
	HomepageURL string   `json:"homepage_url"`
	Categories  []string `json:"categories"`

	// SourceURL is the skill source an install hands to the existing skill
	// import path. Skill entries only.
	SourceURL string `json:"source_url,omitempty"`

	// ConfigTemplate is the MCP entry an install writes into the workspace MCP
	// library, and Placeholders names the values an admin must fill in before
	// it will run. The template itself must never carry a real secret; the
	// placeholder tokens are what the installer substitutes. MCP entries only.
	ConfigTemplate json.RawMessage          `json:"config_template,omitempty"`
	Placeholders   []MarketplacePlaceholder `json:"placeholders,omitempty"`
}

// MarketplacePlaceholder describes one value an MCP install must be given.
// Secret placeholders are stored through the workspace MCP library's existing
// write-only config column and are never returned by any read endpoint.
type MarketplacePlaceholder struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

// marketplaceCatalogFile is the on-disk shape: one file per kind, holding a
// list of items.
type marketplaceCatalogFile struct {
	Items []MarketplaceItem `json:"items"`
}

// MarketplaceCatalog returns every curated entry, sorted by kind then name so
// the listing is stable across processes. A malformed catalog file is a build
// defect rather than a runtime condition, so loading reports the error instead
// of silently shipping a short catalog.
func MarketplaceCatalog() ([]MarketplaceItem, error) {
	entries, err := marketplaceCatalogFS.ReadDir(marketplaceCatalogRoot)
	if err != nil {
		return nil, fmt.Errorf("read marketplace catalog: %w", err)
	}
	var items []MarketplaceItem
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := marketplaceCatalogFS.ReadFile(marketplaceCatalogRoot + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read marketplace catalog %s: %w", entry.Name(), err)
		}
		var file marketplaceCatalogFile
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("parse marketplace catalog %s: %w", entry.Name(), err)
		}
		for _, item := range file.Items {
			if err := validateMarketplaceItem(item); err != nil {
				return nil, fmt.Errorf("marketplace catalog %s: %w", entry.Name(), err)
			}
			if _, dup := seen[item.Key]; dup {
				return nil, fmt.Errorf("marketplace catalog %s: duplicate key %q", entry.Name(), item.Key)
			}
			seen[item.Key] = struct{}{}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

// FindMarketplaceItem returns the catalog entry with this key.
func FindMarketplaceItem(key string) (MarketplaceItem, bool) {
	items, err := MarketplaceCatalog()
	if err != nil {
		return MarketplaceItem{}, false
	}
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return MarketplaceItem{}, false
}

// validateMarketplaceItem rejects an entry that could not be installed, so a
// bad catalog fails at load instead of at the install the user attempted.
func validateMarketplaceItem(item MarketplaceItem) error {
	if strings.TrimSpace(item.Key) == "" {
		return fmt.Errorf("item is missing a key")
	}
	if strings.TrimSpace(item.Name) == "" {
		return fmt.Errorf("item %q is missing a name", item.Key)
	}
	switch item.Kind {
	case MarketplaceKindSkill:
		if strings.TrimSpace(item.SourceURL) == "" {
			return fmt.Errorf("skill item %q is missing source_url", item.Key)
		}
		if len(item.ConfigTemplate) > 0 {
			return fmt.Errorf("skill item %q must not carry an MCP config_template", item.Key)
		}
	case MarketplaceKindMcp:
		if len(item.ConfigTemplate) == 0 {
			return fmt.Errorf("mcp item %q is missing config_template", item.Key)
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(item.ConfigTemplate, &entry); err != nil {
			return fmt.Errorf("mcp item %q has a config_template that is not a JSON object", item.Key)
		}
		if len(entry) == 0 {
			return fmt.Errorf("mcp item %q has an empty config_template", item.Key)
		}
		if item.SourceURL != "" {
			return fmt.Errorf("mcp item %q must not carry a skill source_url", item.Key)
		}
		for _, placeholder := range item.Placeholders {
			if strings.TrimSpace(placeholder.Key) == "" {
				return fmt.Errorf("mcp item %q has a placeholder without a key", item.Key)
			}
		}
	default:
		return fmt.Errorf("item %q has an unknown kind %q", item.Key, item.Kind)
	}
	return nil
}

// RenderMarketplaceMcpConfig substitutes an install's supplied values into the
// catalog template and returns the entry to store in the workspace MCP
// library. Every required placeholder must be supplied; an unknown key is
// rejected rather than ignored, so a typo in a secret name surfaces as an
// error instead of an entry that silently authenticates as nobody.
//
// Substitution is textual over the serialized template, matching `${key}`.
// The rendered result is re-parsed before being returned, so a value that
// would break the JSON shape cannot reach the library.
func RenderMarketplaceMcpConfig(item MarketplaceItem, values map[string]string) (json.RawMessage, error) {
	if item.Kind != MarketplaceKindMcp {
		return nil, fmt.Errorf("item %q is not an MCP item", item.Key)
	}
	allowed := make(map[string]MarketplacePlaceholder, len(item.Placeholders))
	for _, placeholder := range item.Placeholders {
		allowed[placeholder.Key] = placeholder
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("unknown configuration value %q", key)
		}
	}
	rendered := string(item.ConfigTemplate)
	for _, placeholder := range item.Placeholders {
		value, ok := values[placeholder.Key]
		if !ok || strings.TrimSpace(value) == "" {
			if placeholder.Required {
				return nil, fmt.Errorf("%s is required", placeholder.Key)
			}
			value = ""
		}
		// Encode through the JSON string encoder so a value carrying a quote
		// or backslash cannot escape its position in the document.
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode configuration value %q: %w", placeholder.Key, err)
		}
		quoted := string(encoded)
		// The template holds the placeholder inside a JSON string, so the
		// substitution drops the encoder's surrounding quotes.
		rendered = strings.ReplaceAll(rendered, "${"+placeholder.Key+"}", quoted[1:len(quoted)-1])
	}
	if strings.Contains(rendered, "${") {
		return nil, fmt.Errorf("configuration template still has unfilled values")
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rendered), &check); err != nil {
		// Never wrap: the underlying error can echo fragments of an entry that
		// routinely embeds API tokens.
		return nil, fmt.Errorf("rendered configuration is not a JSON object")
	}
	return json.RawMessage(rendered), nil
}
