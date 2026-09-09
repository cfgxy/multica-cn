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
const Revision = "promptscan/2026-09-09.2"

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

	// validate, when set, is handed the match's capture groups plus the rest of
	// the line the match ended on, and decides whether the match is really a
	// credential. It exists because RE2 has no lookahead and the interesting
	// questions here — is the value empty, is it a placeholder, is that token
	// actually the next key — are all decided by what comes AFTER the value.
	//
	// lineRest is passed in rather than captured by the regex on purpose. A
	// capture group would widen the MATCH, and Go's FindAll only resumes after
	// the previous match ends: a match that reached the line ending and was then
	// released took every later field on that line with it, so a real credential
	// sitting beside a schema field was never scanned at all.
	validate func(groups []string, lineRest string) bool
}

// credentialFieldName is the set of field names that mean "what follows is a
// credential", written to tolerate the separators real configs use:
// `DB_PASSWORD`, `db-password` and `dbPassword` all reduce to the same rule.
const credentialFieldName = `(?:api[_\- ]?key|api[_\- ]?secret|secret[_\- ]?key|access[_\- ]?token|refresh[_\- ]?token|auth[_\- ]?token|id[_\- ]?token|client[_\- ]?secret|private[_\- ]?key|database[_\- ]?url|db[_\- ]?password|db[_\- ]?url|redis[_\- ]?url|x[_\- ]?api[_\- ]?key|authorization|proxy[_\- ]?authorization|credential|passphrase|password|passwd|secret|token)`

// quoteRun is one optional quote, preceded by any number of backslashes.
//
// The backslashes are what makes escaped JSON visible. A webhook body, a log
// line or an example pasted into a prompt arrives as `{\"password\":\"real\"}`,
// and a rule that expects a bare quote sees the backslash sitting between the
// field name and the separator and stops matching — so the credential
// published.
const quoteRun = `\\*["'` + "`" + `]?`

// gap is the whitespace allowed between the pieces of one assignment. It spans
// at most a single line break, optionally preceded by a shell continuation
// backslash.
//
// Crossing a line at all is the point: a JSON encoder is free to put the value
// on its own line, and pretty-printers routinely do it, which left the key line
// with no value and the value line with no key so that neither half matched.
// Crossing only ONE is equally the point — `\s*` would let a field name bind to
// a value pages below it, and every prose mention of a password followed
// eventually by a colon would block.
const gap = `[ \t]*(?:\\?\r?\n[ \t]*)?`

// credentialSeparator spans the punctuation between a field name and its value
// across the formats a prompt actually carries: `=` for env files and shell,
// `:` for JSON, YAML and HTTP headers, and `=>`/`:=` for the assignment
// syntaxes that show up in pasted code.
//
// The closing quote of a JSON key is part of this run rather than part of the
// name, which is the bug this replaces: requiring `=` or `:` IMMEDIATELY after
// the field name meant `{"password":"real"}` never matched and shipped into the
// cross-workspace catalog.
// The gap AFTER the punctuation is captured: whether the value sits on the
// field's own line or the next one is what tells a real assignment apart from a
// YAML key whose value is simply the following key. See credentialAssignment.
const credentialSeparator = quoteRun + gap + `(?::=|=>|[=:])` + `(` + gap + `)`

// credentialValue captures the value, in three groups: the opening quote run,
// the value token, and the single separator character after it.
//
// It captures rather than asserts because the decision needs all of them and
// RE2 has no lookahead: an empty token is a schema, an unquoted `null` is a
// schema, and a bare token on the NEXT line followed by its own `:` is the next
// key rather than this key's value — the false positive that looking past a
// line break would otherwise introduce for every YAML document.
//
// It deliberately stops at the token. Placeholder closing needs the characters
// the token dropped (it stops at `}`), but those are read from the line as
// context in credentialAssignment rather than captured here: matching to the
// line ending made one released field consume every later field on that line,
// so `{"password":null,"token":"<real>"}` published.
const credentialValue = `(` + quoteRun + `)([^\s"'` + "`" + `,}\]:=]*)([:=]?)`

// notASecretValue are the tokens that occupy a credential field without being
// a credential: JSON and YAML empties, and the template references that a
// shareable prompt is SUPPOSED to contain. Blocking these would train
// publishers to route around the gate rather than through it.
var notASecretValue = map[string]bool{
	"null": true, "nil": true, "none": true, "~": true,
	"true": true, "false": true, "undefined": true,
}

// placeholderForms are the complete template references a shareable prompt is
// meant to carry, as opening/closing pairs. Only CLOSED syntax counts: `$`, `<`
// and `{` open a placeholder, but they equally open a real secret, so releasing
// on the opener alone published `password: $ecret123`.
var placeholderForms = [][2]string{
	{"${", "}"},
	{"{{", "}}"},
	{"<", ">"},
}

// placeholderTrailing are the characters allowed to follow a closed placeholder
// and still leave it the WHOLE value: the closing quote run and the structural
// punctuation of whatever document the assignment sits in. Anything else means
// the placeholder is merely a prefix — `${A}realsecret` — and the value is not
// a template reference at all.
const placeholderTrailing = " \t\\\"'`,;}])" + "\r\n"

// isCompletePlaceholder reports whether value is exactly one closed template
// reference, ignoring the punctuation that closes the surrounding document.
func isCompletePlaceholder(value string) bool {
	for _, form := range placeholderForms {
		if !strings.HasPrefix(value, form[0]) {
			continue
		}
		rest := value[len(form[0]):]
		end := strings.Index(rest, form[1])
		if end < 0 {
			continue
		}
		tail := rest[end+len(form[1]):]
		if strings.Trim(tail, placeholderTrailing) == "" {
			return true
		}
	}
	return false
}

// valueTerminators end an unquoted value: the next field, the next word or the
// end of the statement.
const valueTerminators = " \t\r\n,;"

// fullValue reassembles the value the match started on, reading past the end of
// the match but stopping at the end of THIS value.
//
// It is needed because the value token stops at `}`, so `${VAR}` and `{{ var }}`
// reach the placeholder test truncated. Reading only to the end of this value —
// rather than to the end of the line — is what keeps a released field from
// speaking for the fields after it on the same line.
func fullValue(quote, token, follow, lineRest string) string {
	value := token + follow + lineRest
	if isQuoted(quote) {
		if end := closingQuote(value, len(token+follow), openingQuote(quote), strings.Count(quote, `\`)); end >= 0 {
			return value[:end]
		}
		return value
	}
	// An unquoted placeholder may contain the spaces that otherwise end an
	// unquoted value (`{{ secret }}`), so its closer is skipped past first. Only
	// the closer is skipped, never what follows it: `${A}realsecret` has to keep
	// its tail, which is what makes it a secret rather than a reference.
	cursor := 0
	for _, form := range placeholderForms {
		if !strings.HasPrefix(value, form[0]) {
			continue
		}
		if end := strings.Index(value[len(form[0]):], form[1]); end >= 0 {
			cursor = len(form[0]) + end + len(form[1])
		}
		break
	}
	if i := strings.IndexAny(value[cursor:], valueTerminators); i >= 0 {
		return value[:cursor+i]
	}
	return value
}

// closingQuote returns the offset in value where the quoted value ends, or -1
// when no closing quote is on this line.
//
// A quote ends the value only when its IDENTITY matches the opening one, and
// identity is two things:
//
//   - the character. `"`, `'` and backtick each quote the other two as ordinary
//     content — a single quote inside a JSON string is legal JSON, so
//     `{"password":"${DB_PASSWORD}'trailing"}` ends at the final `"`, not at the
//     apostrophe, and the trailing text stays inside the value where the
//     placeholder test rejects it.
//   - the escape level. depth is how many backslashes the OPENING quote carried:
//     plain JSON opens with `"` (depth 0), JSON embedded in a JSON string opens
//     with `\"` (depth 1). A quote whose own backslash run is deeper is escaped
//     INSIDE the value, so `{\"password\":\"${DB_PASSWORD}\\\"trailing\"}` ends
//     at the final `\"`.
//
// Any other run length is a shape this revision cannot read, and the value is
// carried to the end of the line so the gate errs towards blocking.
func closingQuote(value string, from int, quote byte, depth int) int {
	for i := from; i < len(value); i++ {
		if value[i] != quote {
			continue
		}
		run := 0
		for j := i - 1; j >= 0 && value[j] == '\\'; j-- {
			run++
		}
		if run == depth {
			return i - run
		}
	}
	return -1
}

// openingQuote is the quote character of a captured quote run, which may also
// hold the backslashes that escaped it.
func openingQuote(quoteRun string) byte {
	if i := strings.IndexAny(quoteRun, "\"'`"); i >= 0 {
		return quoteRun[i]
	}
	return 0
}

// isQuoted reports whether the captured quote run actually contains a quote.
// The run may hold backslashes alone — escaped JSON arrives as `\"` and a lone
// `\` carries no quoting meaning.
func isQuoted(quoteRun string) bool {
	return strings.ContainsAny(quoteRun, "\"'`")
}

// credentialAssignment decides whether a regex match is a real assignment.
// groups are the gap captured by credentialSeparator followed by the three from
// credentialValue; lineRest is the text between the match end and the line
// ending, used as read-only context.
func credentialAssignment(groups []string, lineRest string) bool {
	gapAfter, quote, token, follow := groups[0], groups[1], groups[2], groups[3]

	// No value: `{"password": ""}`, `password:` at the end of a line, or a key
	// whose next line closes the object.
	if token == "" {
		return false
	}
	// `null` and friends are the ABSENCE of a value only when they are bare
	// structured scalars. Quoted, they are ordinary strings that happen to spell
	// a keyword, and releasing them let any secret publish under that name.
	if !isQuoted(quote) && notASecretValue[strings.ToLower(token)] {
		return false
	}
	// `${DB_PASSWORD}`, `<your-token-here>`, `{{ secret }}`. The token stops at
	// `}`, so the closing half lives past the match end and the two are rejoined
	// here — bounded to this value, not to the line.
	if isCompletePlaceholder(fullValue(quote, token, follow, lineRest)) {
		return false
	}
	// A bare token that both sits on the next line and carries its own
	// separator is the next KEY, not this key's value — in
	// `password:\n  host: localhost` the match runs to `host:`, which belongs
	// to `host`. Both conditions are needed: `PASSWORD=a=b` on one line is a
	// real value that happens to contain `=`, and a quoted token is
	// unambiguous, so `"password":\n  "http://x"` must still block.
	if quote == "" && strings.Contains(gapAfter, "\n") && (follow == ":" || follow == "=") {
		return false
	}
	return true
}

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
	{"cookie_header", CategoryCookie, regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_\-])(?:set[_\- ]?)?cookie` + credentialSeparator + credentialValue), credentialAssignment},

	{"aws_access_key_id", CategoryAPIKey, regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), nil},
	{"aws_secret_access_key", CategoryAPIKey, regexp.MustCompile(`(?i)(?:aws[_\- ]?secret[_\- ]?access[_\- ]?key|secret[_\- ]?access[_\- ]?key)` + credentialSeparator + quoteRun + `[A-Za-z0-9/+=]{40}`), nil},
	{"google_api_key", CategoryAPIKey, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), nil},
	{"openai_anthropic_key", CategoryAPIKey, regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), nil},
	{"stripe_live_key", CategoryAPIKey, regexp.MustCompile(`\b(?:sk|rk)_live_[0-9A-Za-z]{16,}\b`), nil},

	{"github_token", CategoryToken, regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,255}\b`), nil},
	{"github_fine_grained_token", CategoryToken, regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`), nil},
	{"gitlab_token", CategoryToken, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`), nil},
	{"slack_token", CategoryToken, regexp.MustCompile(`\bxox[bporase]-[A-Za-z0-9\-]{10,}\b`), nil},
	{"slack_app_token", CategoryToken, regexp.MustCompile(`\bxapp-[A-Za-z0-9-]{10,}\b`), nil},
	{"jwt", CategoryToken, regexp.MustCompile(`\bey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), nil},
	{"bearer_header", CategoryToken, regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]{8,}=*\b`), nil},

	{"private_key_block", CategoryPrivateKey, regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----`), nil},

	{"connection_string_password", CategoryPassword, regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis|amqp)(?:\+srv)?://[^:\s/]+:[^@\s]+@`), nil},
	// One rule for every shape a credential assignment takes: `PASSWORD=x`,
	// `{"password":"x"}`, `password: x`, `X-Api-Key: x`, `"token" => "x"`,
	// each of those with the value on the next line, and each of those escaped.
	// Separating them per format would multiply the rules without adding a
	// detection: what makes this a credential is the field name, not the
	// punctuation around it.
	{"credential_assignment", CategoryPassword, regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_\-])` + credentialFieldName + credentialSeparator + credentialValue), credentialAssignment},

	// PII is a weaker signal than the rest: an email address in a prompt is
	// often a deliberate example. It still blocks, because the alternative is
	// a warning nobody reads publishing a real address to a public catalog —
	// and the publisher can rewrite the line to a placeholder domain.
	{"email_address", CategoryPII, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), nil},
}

// Scan reports every credential-shaped match in content.
//
// It matches against the whole text rather than line by line, because the
// formats being detected are free to put a field name and its value on
// different lines — scanning per line meant a pretty-printed JSON credential
// matched neither half and published. Line numbers are still 1-based and count
// \n, matching what an editor shows the publisher, and point at where the match
// STARTS: the field name, which is what the publisher needs to find. Multi-line
// constructs (a PEM block) are likewise reported at their header line — the
// goal is to point at the paste, not to delimit it.
func Scan(content string) Result {
	res := Result{Revision: Revision}
	seen := make(map[string]struct{})

	for _, d := range detectors {
		for _, m := range d.re.FindAllStringSubmatchIndex(content, -1) {
			if d.validate != nil && !d.validate(captured(content, m), restOfLine(content, m[1])) {
				continue
			}
			lineNo := lineOf(content, m[0])
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

// captured pulls the submatches out of one FindAllStringSubmatchIndex result,
// skipping group 0 (the whole match). An unset group reads as "".
func captured(content string, m []int) []string {
	groups := make([]string, 0, len(m)/2-1)
	for i := 2; i < len(m); i += 2 {
		if m[i] < 0 {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, content[m[i]:m[i+1]])
	}
	return groups
}

// restOfLine is the text from offset up to the next line ending. It is context
// for a validator, never part of the match: widening the match to cover it is
// what let a released field consume the fields after it.
func restOfLine(content string, offset int) string {
	rest := content[offset:]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// lineOf is the 1-based line holding the byte at offset.
func lineOf(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
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
