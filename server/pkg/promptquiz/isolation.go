// Package promptquiz holds the two pure halves of the RUYI-185 quiz mechanism:
// the isolation gate that decides whether a question may be stored, and the
// distribution comparison that decides whether one prompt version's sample
// group differs from another's beyond same-version noise.
//
// Both are deliberately free of database and HTTP: the gate has to be testable
// against a table of rejected bodies, and the comparison has to be replayable
// against a fixed sample so its three pinned values (N, dispersion measure,
// noise line) can be re-derived by anyone reading the test rather than taken
// on trust.
package promptquiz

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A quiz run has no issue (Owner Q18-A): it is enqueued with issue_id NULL so
// that every issue-dimension query, all of which JOIN issue, cannot see it.
// Isolation therefore cannot rely on scoping — the run simply has no production
// context to be scoped to. What is left to guard is the one channel that could
// still carry production data into it: the question body itself.
//
// So the rule is that a body may not NAME a production entity. An agent given
// "summarise issue RUYI-118" would go and read RUYI-118, and the quiz would
// stop measuring the prompt and start measuring that issue's contents — which
// also drift between runs, destroying the comparability the whole mechanism
// exists for. Rejecting the reference is both the isolation control and the
// precondition for a stable measurement.

// EntityRefKind is the coarse bucket reported to the author. Like
// promptscan.Category it is rendered in the UI, so it stays small and stable
// rather than tracking the pattern list one-to-one.
type EntityRefKind string

const (
	// RefUUID is a bare UUID. Every production row in this platform is keyed by
	// one, so a body containing one is naming a row whatever it calls it.
	RefUUID EntityRefKind = "uuid"
	// RefIssueKey is a human issue key such as RUYI-118 or MUL-4302.
	RefIssueKey EntityRefKind = "issue_key"
	// RefMention is a mention:// link. These are side-effecting in this
	// platform — an agent mention enqueues a run — so one inside a quiz body
	// would let a measurement dispatch real work.
	RefMention EntityRefKind = "mention"
	// RefURL is an http(s) link. A question that sends the agent to fetch
	// something measures that endpoint's availability, not the prompt.
	RefURL EntityRefKind = "url"
)

// EntityRef is one rejected reference, stripped of the matched text.
//
// There is no Value field, for the same reason promptscan.Finding has none:
// the body is workspace-private (Owner Q8) and a validation error is the most
// widely echoed surface in the system. Offset is what an author actually needs
// to find it.
type EntityRef struct {
	Kind   EntityRefKind `json:"kind"`
	Offset int           `json:"offset"`
}

// maxRefs bounds a report the same way promptscan bounds its findings: a body
// pasted from a status update would otherwise produce one entry per line, and
// the gate rejects either way.
const maxRefs = 20

// MaxBodyBytes is the ceiling enforced alongside the DDL CHECK. Duplicated on
// purpose: the constraint keeps a direct SQL writer honest, and this keeps the
// API returning a readable error instead of a constraint violation.
const MaxBodyBytes = 4000

var entityPatterns = []struct {
	kind EntityRefKind
	re   *regexp.Regexp
}{
	// Ordered most specific first so a mention link, which embeds a UUID, is
	// reported as a mention rather than as a bare id.
	{RefMention, regexp.MustCompile(`(?i)mention://[a-z]+/[0-9a-f-]+`)},
	{RefURL, regexp.MustCompile(`(?i)https?://[^\s)"'<>]+`)},
	{RefUUID, regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)},
	// Issue keys: two or more uppercase letters, a hyphen, digits. Uppercase is
	// required so ordinary hyphenated words do not trip it. Standard names
	// share the shape (UTF-8, RFC-7231) and are excluded by prefix below
	// rather than by narrowing the pattern — narrowing it enough to miss UTF-8
	// would also miss a real two-digit issue key.
	{RefIssueKey, regexp.MustCompile(`\b[A-Z]{2,}-\d{1,6}\b`)},
}

// standardNamePrefixes are the uppercase-hyphen-digit tokens that name a
// standard rather than a row in this database. Writing a question about text
// encodings or HTTP semantics is ordinary; having the gate forbid it would push
// authors into paraphrase for no isolation benefit, since none of these can be
// resolved into a production entity.
var standardNamePrefixes = map[string]bool{
	"UTF": true, "UCS": true, "ISO": true, "RFC": true, "IEEE": true,
	"SHA": true, "MD": true, "AES": true, "RSA": true, "HTTP": true,
	"IPV": true, "ES": true, "PEP": true, "CVE": true, "ANSI": true,
}

// isStandardName reports whether an issue-key-shaped match is one of the
// standard names above.
func isStandardName(match string) bool {
	prefix, _, ok := strings.Cut(match, "-")
	return ok && standardNamePrefixes[prefix]
}

// Validation is the gate's verdict on one question body.
type Validation struct {
	Refs    []EntityRef `json:"refs"`
	TooLong bool        `json:"too_long"`
	Empty   bool        `json:"empty"`
}

// OK reports whether the body may be stored.
func (v Validation) OK() bool {
	return len(v.Refs) == 0 && !v.TooLong && !v.Empty
}

// Reason is a human-readable refusal that names what was found and where, and
// never echoes the matched text.
func (v Validation) Reason() string {
	switch {
	case v.OK():
		return ""
	case v.Empty:
		return "question body must not be empty"
	case v.TooLong:
		return fmt.Sprintf("question body exceeds %d bytes", MaxBodyBytes)
	}
	parts := make([]string, 0, len(v.Refs))
	for _, r := range v.Refs {
		parts = append(parts, fmt.Sprintf("%s at offset %d", r.Kind, r.Offset))
	}
	return "question body must not reference production entities: " + strings.Join(parts, ", ")
}

// Validate applies the isolation gate to one question body.
//
// A pass means "no pattern of this set matched", never "this question is
// certainly isolated": a body can still describe a real project in prose. What
// the gate guarantees is the mechanical half — the body carries no identifier
// an agent could resolve into a production row.
func Validate(body string) Validation {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return Validation{Empty: true}
	}
	if len(body) > MaxBodyBytes {
		return Validation{TooLong: true}
	}

	var refs []EntityRef
	covered := make([]bool, len(body))
	for _, p := range entityPatterns {
		for _, loc := range p.re.FindAllStringIndex(body, -1) {
			// A UUID inside an already-reported mention link is the same
			// reference seen twice; report the outer one only.
			if covered[loc[0]] {
				continue
			}
			if p.kind == RefIssueKey && isStandardName(body[loc[0]:loc[1]]) {
				continue
			}
			for i := loc[0]; i < loc[1]; i++ {
				covered[i] = true
			}
			refs = append(refs, EntityRef{Kind: p.kind, Offset: loc[0]})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Offset < refs[j].Offset })
	if len(refs) > maxRefs {
		refs = refs[:maxRefs]
	}
	return Validation{Refs: refs}
}
