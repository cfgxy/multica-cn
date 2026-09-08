// Package promptscan is the fail-closed publish gate for marketplace prompt
// content (RUYI-100).
//
// It is deliberately NOT pkg/redact. That package rewrites agent output in
// place and answers "what is safe to store"; it returns a scrubbed string with
// no structure, so a caller cannot say which rule fired, on which line, or that
// anything fired at all. Publishing needs the opposite shape: refuse the write
// and hand the publisher enough to find and remove the credential themselves,
// without the response, the error or the log ever echoing the value.
//
// Honest boundary: a pass means "no rule of this revision matched", never "this
// text contains no secret". Prose secrets ("our internal escalation code is
// ...") are not mechanically detectable, which is why Revision travels with
// every result and why the publish UI still carries a "this becomes public"
// confirmation.
package promptscan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Revision identifies the detector set that produced a result. It is persisted
// with the scan so a future rule addition can tell which snapshots were only
// ever cleared by an older, weaker detector set. Bump it whenever `detectors`
// changes.
const Revision = "promptscan/2026-09-08.2"

// maxFindings bounds a report. Prompt content is unbounded user text, and a
// pasted .env would otherwise produce a finding per line — a response large
// enough to be its own problem. The gate blocks either way, so truncating costs
// the publisher nothing but a second round.
const maxFindings = 50

// Mask is what a finding shows instead of the match. It is a fixed literal, not
// a slice of the matched text: real leading characters would still be content,
// and a low-entropy secret ("PASSWORD=1234") is reconstructible from four of
// them. Location is carried by Line, which is what a publisher actually needs
// to find and remove the credential.
const Mask = "••••"

// Category is the coarse bucket shown to the publisher. It is intentionally
// small and stable: it is rendered in the UI and persisted in scan_result, so
// it must not track the detector list one-to-one.
type Category string

const (
	CategoryToken      Category = "token"
	CategoryCookie     Category = "cookie"
	CategoryPassword   Category = "password"
	CategoryAPIKey     Category = "api_key"
	CategoryPrivateKey Category = "private_key"
	CategoryPII        Category = "pii"
)

// Finding is one match, stripped of the matched text.
//
// There is no Value field and no Excerpt field, and adding one would defeat the
// package: findings are serialised into scan_result, returned in a 422 body and
// written to logs. Mask is always the Mask constant — it is a field rather than
// implied so the JSON shape stays self-describing for the UI.
type Finding struct {
	Category Category `json:"category"`
	Rule     string   `json:"rule"`
	Line     int      `json:"line"`
	Mask     string   `json:"mask"`
}

// Message is a log/error-safe one-liner. Callers should prefer this over
// formatting a Finding themselves, so there is exactly one place where the
// no-raw-value rule has to hold.
func (f Finding) Message() string {
	return fmt.Sprintf("%s (%s) at line %d, near %q", f.Category, f.Rule, f.Line, f.Mask)
}

// Result is the whole outcome of a scan.
type Result struct {
	Revision  string    `json:"revision"`
	Findings  []Finding `json:"findings"`
	Truncated bool      `json:"truncated"`
}

// OK reports whether the content may be published. Fail-closed: any finding
// blocks, and there is deliberately no severity or confidence knob a caller
// could use to publish anyway.
func (r Result) OK() bool { return len(r.Findings) == 0 }

type detector struct {
	rule     string
	category Category
	re       *regexp.Regexp
}

// credentialFieldName is the set of field names that mean "what follows is a
// credential", written to tolerate the separators real configs use:
// `DB_PASSWORD`, `db-password` and `dbPassword` all reduce to the same rule.
const credentialFieldName = `(?:api[_\- ]?key|api[_\- ]?secret|secret[_\- ]?key|access[_\- ]?token|refresh[_\- ]?token|auth[_\- ]?token|id[_\- ]?token|client[_\- ]?secret|private[_\- ]?key|database[_\- ]?url|db[_\- ]?password|db[_\- ]?url|redis[_\- ]?url|x[_\- ]?api[_\- ]?key|authorization|proxy[_\- ]?authorization|credential|passphrase|password|passwd|secret|token)`

// credentialValueStart is what has to follow the separator for the match to be
// a real assignment. It steps over one optional quote and then demands a
// character that is neither whitespace nor a quote, so `"password": ""` and a
// bare `password:` with the value on another line do not report.
const credentialValueStart = `["'` + "`" + `]?[^\s"'` + "`" + `,}\]]`

// credentialSeparator spans the punctuation between a field name and its value
// across the formats a prompt actually carries: `=` for env files and shell,
// `:` for JSON, YAML and HTTP headers, and `=>`/`:=` for the assignment
// syntaxes that show up in pasted code.
//
// The closing quote of a JSON key is part of this run rather than part of the
// name, which is the bug this replaces: requiring `=` or `:` IMMEDIATELY after
// the field name meant `{"password":"real"}` never matched and shipped into the
// cross-workspace catalog.
const credentialSeparator = `["'` + "`" + `]?\s*(?::=|=>|[=:])\s*`

// detectors are all applied to every line; unlike redact.patterns there is no
// first-match-wins, because a line holding both a token and a password should
// report both.
//
// Ordering within the slice only affects report ordering, which Scan sorts
// anyway.
var detectors = []detector{
	// Cookie headers come first only for readability. `session_id=...` would
	// also trip the generic credential rule; dedupe is per (rule, line), so
	// both may report, and that is fine — the publisher removes the line once.
	{"cookie_header", CategoryCookie, regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_\-])(?:set[_\- ]?)?cookie` + credentialSeparator + credentialValueStart)},

	{"aws_access_key_id", CategoryAPIKey, regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"aws_secret_access_key", CategoryAPIKey, regexp.MustCompile(`(?i)(?:aws[_\- ]?secret[_\- ]?access[_\- ]?key|secret[_\- ]?access[_\- ]?key)` + credentialSeparator + `["'` + "`" + `]?[A-Za-z0-9/+=]{40}`)},
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
	// One rule for every shape a credential assignment takes: `PASSWORD=x`,
	// `{"password":"x"}`, `password: x`, `X-Api-Key: x`, `"token" => "x"`.
	// Separating them per format would multiply the rules without adding a
	// detection: what makes this a credential is the field name, not the
	// punctuation around it.
	{"credential_assignment", CategoryPassword, regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_\-])` + credentialFieldName + credentialSeparator + credentialValueStart)},

	// PII is a weaker signal than the rest: an email address in a prompt is
	// often a deliberate example. It still blocks, because the alternative is
	// a warning nobody reads publishing a real address to a public catalog —
	// and the publisher can rewrite the line to a placeholder domain.
	{"email_address", CategoryPII, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
}

// Scan reports every credential-shaped match in content.
//
// Line numbers are 1-based and count \n, matching what an editor shows the
// publisher. Multi-line constructs (a PEM block) are detected by their header
// line only: the goal is to point at the paste, not to delimit it.
func Scan(content string) Result {
	res := Result{Revision: Revision}
	seen := make(map[string]struct{})

	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1
		for _, d := range detectors {
			if !d.re.MatchString(line) {
				continue
			}
			key := d.rule + ":" + fmt.Sprint(lineNo)
			if _, dup := seen[key]; dup {
				continue
			}
			if len(res.Findings) >= maxFindings {
				res.Truncated = true
				sortFindings(res.Findings)
				return res
			}
			seen[key] = struct{}{}
			res.Findings = append(res.Findings, Finding{
				Category: d.category,
				Rule:     d.rule,
				Line:     lineNo,
				Mask:     Mask,
			})
		}
	}

	sortFindings(res.Findings)
	return res
}

// sortFindings orders by line then rule so the report reads top-to-bottom and
// is stable across runs — the value lands in scan_result, and an unstable order
// would make two scans of identical content look different.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Line != f[j].Line {
			return f[i].Line < f[j].Line
		}
		return f[i].Rule < f[j].Rule
	})
}
