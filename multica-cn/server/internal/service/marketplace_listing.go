package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// The published half of the marketplace (RUYI-99).
//
// D2-A: workspace-published listings live in their own table and are merged
// with the embedded static catalog at read time, rather than the static catalog
// being migrated into rows. The static entries are curated platform content
// with no publisher and no lifecycle; giving them a publish state they can
// never leave would be a fiction the whole write path then has to guard.

// MarketplaceListingKeyPrefix distinguishes a published listing's catalog key
// from a static entry's. Static keys are authored in the JSON files and look
// like "skill:anthropics/skills/pdf"; a published key is derived from its row
// id, so the two spaces cannot collide however the files are edited.
const MarketplaceListingKeyPrefix = "listing:"

// MarketplaceListingKey is the catalog key for one published listing.
func MarketplaceListingKey(id string) string {
	return MarketplaceListingKeyPrefix + id
}

// MarketplaceListingIDFromKey recovers the row id from a published listing's
// catalog key. Returns false for a static key, which is how the install path
// tells the two halves apart without a second lookup.
func MarketplaceListingIDFromKey(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, MarketplaceListingKeyPrefix)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// NormalizeMarketplaceName is the D3-A uniqueness normalisation: `kind` plus
// the lowercased name is globally unique across every workspace AND across the
// static catalog. Lowercasing is the whole of it — the install paths already
// restrict a name to letters, digits, hyphens and underscores, so there is no
// unicode folding surface here, and doing more (stripping hyphens, say) would
// make two legitimately different names collide.
func NormalizeMarketplaceName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// StaticMarketplaceNameKeys returns the (kind, normalised name) pairs the
// embedded catalog already occupies, as a set keyed by kind+"/"+nameKey.
//
// Publishing has to reject these too: the merged listing installs by name, so a
// published "filesystem" MCP next to the curated one would give an installer
// two entries that create the same workspace row and collide on the second.
func StaticMarketplaceNameKeys() (map[string]struct{}, error) {
	items, err := MarketplaceCatalog()
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item.Kind+"/"+NormalizeMarketplaceName(item.Name)] = struct{}{}
	}
	return out, nil
}

// MarketplaceCategories is the closed set a published listing may claim.
//
// It is the same vocabulary the curated catalog uses, kept here rather than
// derived from the catalog files: deriving it would make the set shrink the
// moment a curated entry is retired, silently invalidating listings that were
// already published under that category. The client renders the same list, and
// ValidateMarketplaceListingDraft is what makes the client's copy advisory.
var MarketplaceCategories = []string{
	"data",
	"development",
	"documents",
	"files",
	"productivity",
	"web",
}

// IsMarketplaceCategory reports whether a submitted category is in the set.
func IsMarketplaceCategory(category string) bool {
	trimmed := strings.TrimSpace(category)
	for _, known := range MarketplaceCategories {
		if trimmed == known {
			return true
		}
	}
	return false
}

// PublishedMarketplaceListing is one row of the published half, in the shape
// the merge needs. It deliberately omits source_workspace_id: that column is
// withdrawal authority, and the merged catalog is read by every workspace.
type PublishedMarketplaceListing struct {
	ID                   string
	Kind                 string
	Name                 string
	PublisherDisplayName string
	Summary              string
	Description          string
	HomepageURL          string
	Categories           []string
	SourceURL            string
	ConfigTemplate       json.RawMessage
	Placeholders         []MarketplacePlaceholder
}

// ToMarketplaceItem projects a published listing into the same shape the static
// catalog uses, so everything downstream — filtering, installed-state
// annotation, install, the response type — has exactly one item type to handle.
func (l PublishedMarketplaceListing) ToMarketplaceItem() MarketplaceItem {
	item := MarketplaceItem{
		Key:          MarketplaceListingKey(l.ID),
		Kind:         l.Kind,
		Name:         l.Name,
		Summary:      l.Summary,
		Description:  l.Description,
		Publisher:    l.PublisherDisplayName,
		HomepageURL:  l.HomepageURL,
		Categories:   l.Categories,
		SourceURL:    l.SourceURL,
		Placeholders: l.Placeholders,
	}
	if l.Kind == MarketplaceKindMcp {
		item.ConfigTemplate = l.ConfigTemplate
	}
	return item
}

// MergeMarketplaceItems interleaves the static catalog with published listings,
// sorted by kind then name so the merged listing is stable across processes and
// matches the order MarketplaceCatalog already returns.
//
// A published listing whose (kind, name) duplicates a static entry is dropped
// rather than shadowing it. The publish path rejects such a name, so reaching
// this branch means the static catalog gained an entry after a listing was
// published — in which case the curated one wins, because it is the entry the
// platform ships and supports.
func MergeMarketplaceItems(static []MarketplaceItem, published []MarketplaceItem) []MarketplaceItem {
	occupied := make(map[string]struct{}, len(static))
	for _, item := range static {
		occupied[item.Kind+"/"+NormalizeMarketplaceName(item.Name)] = struct{}{}
	}
	merged := make([]MarketplaceItem, 0, len(static)+len(published))
	merged = append(merged, static...)
	for _, item := range published {
		if _, clash := occupied[item.Kind+"/"+NormalizeMarketplaceName(item.Name)]; clash {
			continue
		}
		merged = append(merged, item)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Kind != merged[j].Kind {
			return merged[i].Kind < merged[j].Kind
		}
		return NormalizeMarketplaceName(merged[i].Name) < NormalizeMarketplaceName(merged[j].Name)
	})
	return merged
}

// ValidateMarketplaceListingDraft checks a listing a workspace is about to
// publish. It reuses validateMarketplaceItem for the kind-specific shape rules
// so a published entry cannot be installable in ways a curated one is not, and
// adds the rules that only apply to user-submitted content.
//
// The error text names the offending field and never quotes its value: a
// rejected MCP template is exactly the place a token would be.
func ValidateMarketplaceListingDraft(item MarketplaceItem) error {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 100 {
		return fmt.Errorf("name must be at most 100 characters")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("name may only contain letters, digits, hyphens, and underscores")
		}
	}
	// A summary is what the catalog row shows: an entry published without one
	// is indistinguishable from a broken import to every workspace browsing it.
	if strings.TrimSpace(item.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if len(item.Summary) > 200 {
		return fmt.Errorf("summary must be at most 200 characters")
	}
	if len(item.Description) > 2000 {
		return fmt.Errorf("description must be at most 2000 characters")
	}
	if len(item.Categories) == 0 {
		return fmt.Errorf("at least one category is required")
	}
	if len(item.Categories) > 10 {
		return fmt.Errorf("a listing may carry at most 10 categories")
	}
	for _, category := range item.Categories {
		// Free text would fragment the catalog's only navigation axis, so the
		// set is closed and shared with the client rather than validated by
		// length alone.
		if !IsMarketplaceCategory(category) {
			return fmt.Errorf("categories must be chosen from the marketplace category list")
		}
	}
	if err := validatePublicHTTPURL("homepage_url", item.HomepageURL, false); err != nil {
		return err
	}

	switch item.Kind {
	case MarketplaceKindSkill:
		// A skill listing publishes a PUBLIC address, never the publisher's
		// stored bytes. Requiring an https URL here is what keeps publishing
		// from becoming a way to copy a private workspace's skill content into
		// a catalog every workspace reads.
		if err := validatePublicHTTPURL("source_url", item.SourceURL, true); err != nil {
			return err
		}
	case MarketplaceKindMcp:
		if err := validateMarketplaceListingTemplate(item); err != nil {
			return err
		}
	default:
		return fmt.Errorf("kind must be %q or %q", MarketplaceKindSkill, MarketplaceKindMcp)
	}

	// The shared shape rules the curated catalog is held to, so a published
	// entry cannot be installable in a way a curated one is not. A draft has no
	// key yet — it is derived from the row id, which the insert assigns — so the
	// shared check runs against a copy carrying a fixed stand-in key. The name
	// is NOT used: validateMarketplaceItem quotes the key in its errors, draft
	// validation runs before the secret scanner, and a publisher who typed a
	// credential into the name field would otherwise have it echoed back in the
	// 400 body.
	shaped := item
	shaped.Key = MarketplaceListingKey("draft")
	return validateMarketplaceItem(shaped)
}

// validateMarketplaceListingTemplate checks an MCP listing's template and the
// placeholders it declares.
//
// The transport check is the D-scope boundary: stdio / http / sse are the three
// the runtime accepts, and the template must declare one it can actually run.
// It is checked against the same classification mcpTransportOf performs, but
// expressed here so publishing does not depend on the HTTP layer.
func validateMarketplaceListingTemplate(item MarketplaceItem) error {
	if len(item.ConfigTemplate) == 0 {
		return fmt.Errorf("config_template is required for an MCP listing")
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(item.ConfigTemplate, &entry); err != nil {
		// Never wrap: the underlying error can echo fragments of a template
		// that routinely embeds credential placeholders next to real values.
		return fmt.Errorf("config_template must be a JSON object")
	}
	if len(entry) == 0 {
		return fmt.Errorf("config_template must not be empty")
	}

	// Runs before anything decodes a field: encoding/json reports no error for
	// a null, so every typed check below would otherwise pass a null through.
	if err := rejectNullTemplateSchemaFields(entry); err != nil {
		return err
	}

	transport, err := marketplaceTemplateTransport(entry)
	if err != nil {
		return err
	}
	switch transport {
	case "stdio":
		if err := requireTemplateString(entry, "command"); err != nil {
			return err
		}
	case "http", "sse":
		if err := requireTemplateURL(entry, "url"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("transport must be one of stdio, http, or sse")
	}
	// The transport's own field is only half the shape. `args`, `env` and
	// `headers` are what the runtime actually launches or dials with, so a
	// template whose containers hold the wrong JSON type would publish and
	// install cleanly and then fail at launch in every workspace that took it.
	// Rejecting here is what makes "installable in another workspace" a
	// property of publishing rather than of luck.
	if err := validateTemplateOptionalFields(entry); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(item.Placeholders))
	for i, placeholder := range item.Placeholders {
		// Placeholder keys are publisher input and draft validation runs BEFORE
		// the secret scanner, so every message below addresses the placeholder
		// by its position in the submitted list and never quotes the key.
		key := strings.TrimSpace(placeholder.Key)
		if key == "" {
			return fmt.Errorf("every placeholder needs a key")
		}
		if !marketplacePlaceholderKeyPattern.MatchString(key) {
			return fmt.Errorf("placeholder #%d has a key that is empty, longer than 64 characters, or carries something other than letters, digits, hyphens, underscores, and dots", i+1)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("placeholder #%d repeats a key already declared", i+1)
		}
		seen[key] = struct{}{}
		if len(placeholder.Label) > 100 {
			return fmt.Errorf("placeholder #%d has a label longer than 100 characters", i+1)
		}
		if len(placeholder.Description) > 300 {
			return fmt.Errorf("placeholder #%d has a description longer than 300 characters", i+1)
		}
		// A declared placeholder the template never references would show the
		// installer an input that goes nowhere.
		if !strings.Contains(string(item.ConfigTemplate), "${"+key+"}") {
			return fmt.Errorf("placeholder #%d is declared but never used in config_template", i+1)
		}
	}
	if len(item.Placeholders) > 20 {
		return fmt.Errorf("a listing may declare at most 20 placeholders")
	}
	return nil
}

// unmarshalTemplateString decodes one template leaf as a string. `null` is
// rejected explicitly: encoding/json treats unmarshalling null as a no-op, so
// without this a `"command": null` would pass a plain string decode and publish
// as an entry with no command at all.
func unmarshalTemplateString(raw json.RawMessage, out *string) error {
	if string(bytes.TrimSpace(raw)) == "null" {
		return fmt.Errorf("value must not be null")
	}
	return json.Unmarshal(raw, out)
}

// marketplaceTemplateSchemaFields lists every key this validator gives a
// declared JSON type. Keys outside the list are publisher extras the runtime
// tolerates, so they are neither type-checked nor null-checked.
var marketplaceTemplateSchemaFields = []string{
	"type", "command", "url", "args", "env", "headers", "cwd", "name", "disabled", "timeout",
}

// rejectNullTemplateSchemaFields fails a template that declares a schema field
// as JSON null. encoding/json never errors on a null: it nils a map or slice
// and leaves a string, bool or number at its zero value, so a per-type decode
// cannot tell `"env": null` from an absent `env`. Publishing such a template
// would install an entry the runtime launches with the field silently dropped.
// Only the field name — fixed by the schema, never publisher content — appears
// in the message, because draft validation runs before the secret scanner.
func rejectNullTemplateSchemaFields(entry map[string]json.RawMessage) error {
	for _, field := range marketplaceTemplateSchemaFields {
		raw, ok := entry[field]
		if !ok {
			continue
		}
		if string(bytes.TrimSpace(raw)) == "null" {
			return fmt.Errorf("config_template %s must not be null", field)
		}
	}
	return nil
}

// validateTemplateOptionalFields type-checks the schema fields a template may
// carry beyond its transport marker. Unknown keys are left alone: the runtime
// tolerates extra keys, and rejecting them would make publishing narrower than
// the config format a workspace can already write by hand.
//
// No message names a publisher-supplied key or value — `env` and `headers` keys
// are publisher content, and this runs before the secret scanner.
func validateTemplateOptionalFields(entry map[string]json.RawMessage) error {
	for _, field := range []string{"cwd", "name"} {
		if raw, ok := entry[field]; ok {
			var s string
			if err := unmarshalTemplateString(raw, &s); err != nil {
				return fmt.Errorf("config_template %s must be a string", field)
			}
		}
	}
	if raw, ok := entry["args"]; ok {
		var args []json.RawMessage
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("config_template args must be an array of strings")
		}
		for _, arg := range args {
			var s string
			if err := unmarshalTemplateString(arg, &s); err != nil {
				return fmt.Errorf("config_template args must be an array of strings")
			}
		}
	}
	for _, field := range []string{"env", "headers"} {
		raw, ok := entry[field]
		if !ok {
			continue
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("config_template %s must be an object whose values are strings", field)
		}
		for _, value := range values {
			var s string
			if err := unmarshalTemplateString(value, &s); err != nil {
				return fmt.Errorf("config_template %s must be an object whose values are strings", field)
			}
		}
	}
	if raw, ok := entry["disabled"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("config_template disabled must be a boolean")
		}
	}
	if raw, ok := entry["timeout"]; ok {
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("config_template timeout must be a number")
		}
	}
	return nil
}

// requireTemplateString demands a present, non-blank string. A field holding a
// number, an object or an empty string parses as JSON but cannot be launched.
func requireTemplateString(entry map[string]json.RawMessage, field string) error {
	raw, ok := entry[field]
	if !ok || len(raw) == 0 {
		return fmt.Errorf("a stdio MCP listing must declare a %s", field)
	}
	var value string
	if err := unmarshalTemplateString(raw, &value); err != nil {
		return fmt.Errorf("config_template %s must be a string", field)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("config_template %s must not be empty", field)
	}
	return nil
}

// requireTemplateURL demands a non-blank http(s) endpoint. A value that still
// contains a `${placeholder}` is only checked for emptiness: the installer
// supplies the rest, and parsing a half-rendered URL would reject templates
// that are correct by design.
func requireTemplateURL(entry map[string]json.RawMessage, field string) error {
	raw, ok := entry[field]
	if !ok || len(raw) == 0 {
		return fmt.Errorf("an http or sse MCP listing must declare a %s", field)
	}
	var value string
	if err := unmarshalTemplateString(raw, &value); err != nil {
		return fmt.Errorf("config_template %s must be a string", field)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("config_template %s must not be empty", field)
	}
	if strings.Contains(trimmed, "${") {
		return nil
	}
	// Reuses the published-URL rule so a template cannot dial a scheme the
	// listing's own homepage_url would be refused for. The value is never
	// echoed.
	return validatePublicHTTPURL("config_template "+field, trimmed, true)
}

// marketplaceTemplateTransport classifies a template the way mcpTransportOf
// classifies a stored entry: an explicit `type` wins, otherwise the presence of
// `command` or `url` decides.
func marketplaceTemplateTransport(entry map[string]json.RawMessage) (string, error) {
	if raw, ok := entry["type"]; ok {
		var declared string
		if err := json.Unmarshal(raw, &declared); err != nil {
			return "", fmt.Errorf("config_template type must be a string")
		}
		switch strings.ToLower(strings.TrimSpace(declared)) {
		case "local", "stdio":
			return "stdio", nil
		case "remote", "http", "streamable-http":
			return "http", nil
		case "sse":
			// Preserved rather than folded into http: the runtime dials an SSE
			// endpoint differently, and rewriting the declared type at publish
			// time would silently change what an installer runs.
			return "sse", nil
		default:
			return "", fmt.Errorf("transport must be one of stdio, http, or sse")
		}
	}
	if len(entry["command"]) > 0 {
		return "stdio", nil
	}
	if len(entry["url"]) > 0 {
		return "http", nil
	}
	return "", fmt.Errorf("config_template must declare a type, a command, or a url")
}

// marketplacePlaceholderKeyPattern restricts a placeholder key to what
// RenderMarketplaceMcpConfig substitutes textually. A key carrying regex or
// JSON metacharacters would let a publisher aim a substitution at a part of the
// template the installer never sees named.
var marketplacePlaceholderKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// validatePublicHTTPURL checks a URL a listing publishes. `required` says
// whether an empty value is acceptable.
//
// Only http(s) is allowed and only with a host: a `file://` or `javascript:`
// value would be rendered as a link in every workspace's catalog. The URL is
// named in the error but never echoed — a source URL can carry a token in its
// query string.
func validatePublicHTTPURL(field, raw string, required bool) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > 500 {
		return fmt.Errorf("%s must be at most 500 characters", field)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL", field)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%s must be an http or https URL", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	if parsed.User != nil {
		// Credentials in a URL are a leak the shape detectors would only
		// sometimes catch, and they would be published verbatim.
		return fmt.Errorf("%s must not embed credentials", field)
	}
	return nil
}
