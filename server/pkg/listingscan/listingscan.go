// Package listingscan is the fail-closed publish gate for marketplace skill and
// MCP listings (RUYI-99).
//
// It differs from pkg/redact, which rewrites agent output in place and returns
// a scrubbed string with no structure: publishing needs to refuse the write and
// tell the publisher WHICH field carries the credential, without the response,
// the error or the log ever echoing the value.
//
// It also differs from the prompt scanner in what it scans. A prompt is one
// blob of prose; a listing is a set of named fields plus, for MCP, a config
// template whose whole purpose is to carry credential-SHAPED strings — as
// `${placeholder}` tokens. So this package scans field by field, reports the
// field name, and treats a registered placeholder token as the only acceptable
// form of a credential-bearing value.
//
// Honest boundary: a pass means "no rule of this revision matched", never "this
// listing contains no secret". A hand-typed password that looks like a word is
// not mechanically detectable, which is why Revision travels with every result
// and why the publish wizard still carries a "this becomes public" confirmation.
package listingscan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Revision identifies the detector set that produced a result. It is persisted
// with the scan so a future rule addition can tell which listings were only
// ever cleared by an older, weaker detector set. Bump it whenever `detectors`
// or the template rules below change.
const Revision = "listingscan/2026-09-10"

// maxFindings bounds a report. A pasted .env in a description would otherwise
// produce a finding per line. The gate blocks either way, so truncating costs
// the publisher nothing but a second round.
const maxFindings = 50

// Mask is what a finding shows instead of the match. It is a fixed literal, not
// a slice of the matched text: a low-entropy secret is reconstructible from a
// few leading characters. Field and Line are what a publisher actually needs to
// find and remove the credential.
const Mask = "••••"

// Category is the coarse bucket shown to the publisher. It is rendered in the
// UI and persisted in scan_result, so it is intentionally small and stable and
// does not track the detector list one-to-one.
type Category string

const (
	CategoryToken       Category = "token"
	CategoryCookie      Category = "cookie"
	CategoryPassword    Category = "password"
	CategoryAPIKey      Category = "api_key"
	CategoryPrivateKey  Category = "private_key"
	CategoryPII         Category = "pii"
	CategoryPlaceholder Category = "placeholder"
)

// Finding is one match, stripped of the matched text.
//
// There is no Value field and no Excerpt field, and adding one would defeat the
// package: findings are serialised into scan_result, returned in a 422 body and
// written to logs. Field is a structural path ("description",
// "config_template.headers[0]"), never a value — and never a publisher-supplied
// JSON key either, because an object key is as good a place to hide a
// credential as its value.
type Finding struct {
	Category Category `json:"category"`
	Rule     string   `json:"rule"`
	Field    string   `json:"field"`
	// Line is 1-based within the field, or 0 for a field that is not
	// line-structured (a JSON template leaf).
	Line int    `json:"line"`
	Mask string `json:"mask"`
}

// Message is a log/error-safe one-liner. Callers should prefer this over
// formatting a Finding themselves, so there is exactly one place where the
// no-raw-value rule has to hold.
func (f Finding) Message() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s (%s) in %s at line %d, near %q", f.Category, f.Rule, f.Field, f.Line, f.Mask)
	}
	return fmt.Sprintf("%s (%s) in %s, near %q", f.Category, f.Rule, f.Field, f.Mask)
}

// Result is the whole outcome of a scan.
type Result struct {
	Revision  string    `json:"revision"`
	Findings  []Finding `json:"findings"`
	Truncated bool      `json:"truncated"`
}

// OK reports whether the listing may be published. Fail-closed: any finding
// blocks, and there is deliberately no severity or confidence knob a caller
// could use to publish anyway.
func (r Result) OK() bool { return len(r.Findings) == 0 }

type detector struct {
	rule     string
	category Category
	re       *regexp.Regexp
}

// detectors are all applied to every line; there is no first-match-wins,
// because a line holding both a token and a password should report both.
//
// Ordering within the slice only affects report ordering, which Scan sorts
// anyway.
var detectors = []detector{
	{"cookie_header", CategoryCookie, regexp.MustCompile(`(?i)\b(?:set-)?cookie\s*:\s*\S+`)},

	{"aws_access_key_id", CategoryAPIKey, regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"aws_secret_access_key", CategoryAPIKey, regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret_?access_?key)\s*[=:]\s*[A-Za-z0-9/+=]{40}`)},
	{"google_api_key", CategoryAPIKey, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"openai_anthropic_key", CategoryAPIKey, regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"stripe_live_key", CategoryAPIKey, regexp.MustCompile(`\b(?:sk|rk)_live_[0-9A-Za-z]{16,}\b`)},

	{"github_token", CategoryToken, regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,255}\b`)},
	{"github_fine_grained_token", CategoryToken, regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`)},
	{"gitlab_token", CategoryToken, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`)},
	{"slack_token", CategoryToken, regexp.MustCompile(`\bxox[bporase]-[A-Za-z0-9\-]{10,}\b`)},
	{"slack_app_token", CategoryToken, regexp.MustCompile(`\bxapp-[A-Za-z0-9-]{10,}\b`)},
	{"jwt", CategoryToken, regexp.MustCompile(`\bey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"bearer_header", CategoryToken, regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]{8,}=*\b`)},

	{"private_key_block", CategoryPrivateKey, regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----`)},

	{"connection_string_password", CategoryPassword, regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis|amqp)(?:\+srv)?://[^:\s/]+:[^@\s]+@`)},
	{"credential_assignment", CategoryPassword, regexp.MustCompile(`(?i)\b(?:API_KEY|API_SECRET|SECRET_KEY|SECRET|ACCESS_TOKEN|AUTH_TOKEN|PRIVATE_KEY|DATABASE_URL|DB_PASSWORD|DB_URL|REDIS_URL|PASSWORD|PASSWD|TOKEN)\s*[=:]\s*\S+`)},

	// PII is a weaker signal than the rest: an email address is often a
	// deliberate example. It still blocks, because the alternative is a warning
	// nobody reads publishing a real address to a catalog every workspace can
	// read — and the publisher can rewrite it to a placeholder domain.
	{"email_address", CategoryPII, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
}

// placeholderToken matches one whole `${key}` reference. A template leaf that
// is exactly a placeholder is the sanctioned way to carry a credential, so it
// is exempt from the detectors.
var placeholderToken = regexp.MustCompile(`\$\{([A-Za-z0-9_.\-]+)\}`)

// wholePlaceholder matches a leaf that is nothing but one placeholder.
var wholePlaceholder = regexp.MustCompile(`^\s*\$\{[A-Za-z0-9_.\-]+\}\s*$`)

// schemaKeys are the MCP entry keys defined by the config schema rather than by
// the publisher. Only these may appear verbatim in a Finding.Field: every other
// object key is publisher-supplied (a header name, an environment variable
// name) and is reported by sorted position instead, because a key is as good a
// place to hide a credential as a value.
var schemaKeys = map[string]struct{}{
	"args":      {},
	"command":   {},
	"cwd":       {},
	"disabled":  {},
	"env":       {},
	"headers":   {},
	"name":      {},
	"timeout":   {},
	"transport": {},
	"type":      {},
	"url":       {},
}

// valueContainers are the template keys whose children are, by schema, values
// the installer supplies. Every non-empty value inside one must be a whole
// registered `${placeholder}` — no key-shape heuristic and no detector match is
// required, because a short internal token looks like nothing in particular and
// a header named `X-Team` carries a credential just as well as one named
// `Authorization`.
var valueContainers = map[string]struct{}{
	"env":     {},
	"headers": {},
}

// collector accumulates findings under the truncation cap, deduplicated by
// (rule, field, line).
type collector struct {
	res  Result
	seen map[string]struct{}
	full bool
}

func newCollector() *collector {
	return &collector{res: Result{Revision: Revision}, seen: map[string]struct{}{}}
}

func (c *collector) add(category Category, rule, field string, line int) {
	if c.full {
		return
	}
	key := rule + "\x00" + field + "\x00" + fmt.Sprint(line)
	if _, dup := c.seen[key]; dup {
		return
	}
	if len(c.res.Findings) >= maxFindings {
		c.res.Truncated = true
		c.full = true
		return
	}
	c.seen[key] = struct{}{}
	c.res.Findings = append(c.res.Findings, Finding{
		Category: category,
		Rule:     rule,
		Field:    field,
		Line:     line,
		Mask:     Mask,
	})
}

// scanText applies every detector to each line of a free-text field.
func (c *collector) scanText(field, content string) {
	for i, line := range strings.Split(content, "\n") {
		for _, d := range detectors {
			if d.re.MatchString(line) {
				c.add(d.category, d.rule, field, i+1)
			}
		}
	}
}

// Placeholder is one registered placeholder as the publisher authored it. All
// three fields are public: the key is echoed by the install dialog and the
// label and description are shown to every installer, so all three are scanned
// like any other free-text field.
type Placeholder struct {
	Key         string
	Label       string
	Description string
}

// Input is the listing being published, in the shape the handler holds it.
// ConfigTemplate is the raw MCP entry; Placeholders are what the publisher
// registered, whose keys are what makes a `${key}` reference legitimate.
type Input struct {
	Name           string
	Summary        string
	Description    string
	HomepageURL    string
	Categories     []string
	SourceURL      string
	ConfigTemplate json.RawMessage
	Placeholders   []Placeholder
}

// Scan reports every credential-shaped match in a listing.
//
// Free-text fields are scanned line by line, and that includes each
// placeholder's key, label and description: all three are published verbatim.
// The MCP template is walked leaf by leaf: a leaf that is exactly a registered
// `${placeholder}` passes untouched, a leaf referencing an UNREGISTERED
// placeholder is a finding (the installer would have no way to fill it, and
// RenderMarketplaceMcpConfig would reject it at install time — far too late),
// and every non-empty value inside `headers` or `env` must be a whole
// registered placeholder regardless of what the shape detectors think of it.
//
// Object keys inside the template are scanned as content too, since a header
// name is publisher-supplied text that reaches the catalog.
func Scan(in Input) Result {
	c := newCollector()

	c.scanText("name", in.Name)
	c.scanText("summary", in.Summary)
	c.scanText("description", in.Description)
	c.scanText("homepage_url", in.HomepageURL)
	c.scanText("source_url", in.SourceURL)
	for i, category := range in.Categories {
		c.scanText(fmt.Sprintf("categories[%d]", i), category)
	}

	declared := make(map[string]struct{}, len(in.Placeholders))
	for i, p := range in.Placeholders {
		declared[p.Key] = struct{}{}
		// Positional field names: the placeholder key is publisher text and
		// must not be echoed back in a finding.
		c.scanText(fmt.Sprintf("placeholders[%d].key", i), p.Key)
		c.scanText(fmt.Sprintf("placeholders[%d].label", i), p.Label)
		c.scanText(fmt.Sprintf("placeholders[%d].description", i), p.Description)
	}

	if len(in.ConfigTemplate) > 0 {
		var doc any
		if err := json.Unmarshal(in.ConfigTemplate, &doc); err != nil {
			// A template that does not parse is rejected by the handler's own
			// shape validation; reporting it here too would duplicate the
			// message without adding information. Never wrap the parse error:
			// it can echo fragments of the template.
			c.add(CategoryPlaceholder, "config_template_unparseable", "config_template", 0)
		} else {
			c.walkTemplate("config_template", doc, declared, false)
		}
	}

	sortFindings(c.res.Findings)
	return c.res
}

// walkTemplate descends the parsed template. `inValueContainer` records whether
// we are inside `headers` or `env`, where every non-empty value must be a whole
// registered placeholder.
//
// Map children are visited in sorted key order and addressed by that position,
// never by the key itself: the ordering is what makes `headers[1]` mean the
// same thing on two scans of identical content.
func (c *collector) walkTemplate(path string, node any, declared map[string]struct{}, inValueContainer bool) {
	switch v := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if _, schema := schemaKeys[key]; schema {
				childPath = path + "." + key
			} else {
				// A publisher-supplied key is content in its own right.
				c.scanKey(childPath+".key", key)
			}
			_, container := valueContainers[key]
			c.walkTemplate(childPath, v[key], declared, inValueContainer || container)
		}
	case []any:
		for i, child := range v {
			c.walkTemplate(fmt.Sprintf("%s[%d]", path, i), child, declared, inValueContainer)
		}
	case string:
		c.scanTemplateLeaf(path, v, declared, inValueContainer)
	default:
		// Numbers, booleans and nulls cannot carry a credential in any form
		// this package can recognise, and `disabled: true` is a switch, not a
		// leak. A value container holding a non-string is caught by the
		// handler's own template shape validation.
	}
}

// scanKey applies the shape detectors to an object key. Keys are not
// line-structured, so findings land on line 0.
func (c *collector) scanKey(field, key string) {
	for _, d := range detectors {
		if d.re.MatchString(key) {
			c.add(d.category, d.rule, field, 0)
		}
	}
}

func (c *collector) scanTemplateLeaf(path, value string, declared map[string]struct{}, inValueContainer bool) {
	// Every referenced placeholder must be one the publisher registered.
	refs := placeholderToken.FindAllStringSubmatch(value, -1)
	for _, ref := range refs {
		if _, ok := declared[ref[1]]; !ok {
			c.add(CategoryPlaceholder, "undeclared_placeholder", path, 0)
		}
	}
	if wholePlaceholder.MatchString(value) {
		// A whole registered placeholder is the sanctioned carrier. If it was
		// undeclared the finding above already fired.
		return
	}
	if inValueContainer && strings.TrimSpace(value) != "" {
		// Inside `headers` or `env`, anything that is not a whole registered
		// placeholder blocks. No key-shape guess and no detector hit is
		// required: that is what makes the rule fail-closed.
		c.add(CategoryToken, "container_literal_value", path, 0)
	}
	for _, d := range detectors {
		if d.re.MatchString(value) {
			c.add(d.category, d.rule, path, 0)
		}
	}
}

// sortFindings orders by field, then line, then rule so the report is stable
// across runs — the value lands in scan_result, and an unstable order would
// make two scans of identical content look different.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Field != f[j].Field {
			return f[i].Field < f[j].Field
		}
		if f[i].Line != f[j].Line {
			return f[i].Line < f[j].Line
		}
		return f[i].Rule < f[j].Rule
	})
}
