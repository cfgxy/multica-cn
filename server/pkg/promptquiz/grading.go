package promptquiz

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The grading pass (RUYI-286): machine-checkable assertions — checks — stored
// on the item's private half and evaluated against what the measured run
// actually answered.
//
// Everything here is deliberately free of database and HTTP, for the same
// reason the isolation gate is: the evaluation has to be replayable against a
// fixed answer so every verdict can be re-derived rather than trusted, and the
// write-side validation has to be testable against a table of rejected shapes.
//
// Two boundaries this package enforces that the rest of the grading chain
// relies on:
//
//   - Evidence never quotes the answer. A verdict names the assertion and what
//     it found (a missing phrase, a boolean, a length) — the answer text stays
//     in task_message, joined via task_id. score_detail therefore cannot
//     become a second copy of the transcript.
//   - Phrases go through the same entity gate as bodies and rubrics. A check
//     phrase that named a production row would make the grade depend on that
//     row's current contents — the drift the body and rubric gates already
//     keep out, arriving on the grading side.

// Check kinds. The set is deliberately small: every kind must be mechanically
// decidable from the answer text alone, with evidence a reader can re-trace. A
// kind that needs judgement ("was the answer polite?") does not belong here —
// it belongs in the free-text rubric, which explains without grading.
const (
	// CheckIncludesAll: every phrase appears in the answer (case-insensitive).
	CheckIncludesAll = "includes_all"
	// CheckIncludesAny: at least one phrase appears (case-insensitive).
	CheckIncludesAny = "includes_any"
	// CheckExcludes: none of the phrases appear (case-insensitive). The
	// forbidden-behaviour half — a refusal question asserts the answer does
	// NOT comply, and a format question asserts a marker is absent.
	CheckExcludes = "excludes"
	// CheckRegex: the answer matches the pattern (RE2, as everywhere else in
	// this codebase; Go's regexp is RE2 and refuses backreferences at compile
	// time, so a stored pattern cannot be quadratic).
	CheckRegex = "regex"
	// CheckMaxChars: the answer is at most limit characters (runes, not bytes —
	// the answer is user-visible text and a CJK answer halves under a byte
	// count).
	CheckMaxChars = "max_chars"
	// CheckMinChars: the answer is at least limit characters (runes).
	CheckMinChars = "min_chars"
)

// checkKinds is the write gate's whitelist. An unknown kind is refused at
// write time rather than skipped at grade time: a check nobody can evaluate
// would silently deflate the score's denominator and make two items with the
// same visible checks grade differently.
var checkKinds = map[string]bool{
	CheckIncludesAll: true,
	CheckIncludesAny: true,
	CheckExcludes:    true,
	CheckRegex:       true,
	CheckMaxChars:    true,
	CheckMinChars:    true,
}

const (
	// MaxChecks bounds one item's assertion set. A bank item is one question,
	// not a compliance suite; past a dozen assertions the weight ratios stop
	// being readable anyway.
	MaxChecks = 32
	// MaxChecksBytes bounds the serialized array, mirroring the body/rubric
	// ceilings: one item must not dominate collection time.
	MaxChecksBytes = 16384
	// MaxCheckWeight bounds one assertion's leverage over the ratio. A weight
	// of 1000 next to weights of 1 is a hidden pass/fail dressed as a ratio.
	MaxCheckWeight = 100
	// DefaultCheckWeight applies when the author omits the field — the common
	// case for a set of equal-strength assertions.
	DefaultCheckWeight = 1
	// MaxPhrases bounds one assertion's phrase list.
	MaxPhrases = 16
)

// Check is one machine-checkable assertion.
//
// The shape is one struct with optional fields rather than per-kind payloads:
// six kinds do not justify a discriminator dance, and the write gate below
// rejects any field combination that does not fit its kind, so the JSON stays
// small and the evaluation total.
type Check struct {
	// ID is the stable handle score_detail and the UI carry back to the
	// assertion, so a verdict can be discussed without quoting its phrases.
	// Required and unique within the item.
	ID string `json:"id"`
	// Kind is one of the Check* constants.
	Kind string `json:"kind"`
	// Weight is the assertion's leverage on the ratio. Zero means "omitted"
	// and grades as DefaultCheckWeight; the write gate refuses values outside
	// (0, MaxCheckWeight].
	Weight float64 `json:"weight,omitempty"`
	// Phrases drives includes_all / includes_any / excludes. At least one
	// non-empty phrase for those kinds; refused for the others.
	Phrases []string `json:"phrases,omitempty"`
	// Pattern drives regex. Must compile as RE2; refused for the others.
	Pattern string `json:"pattern,omitempty"`
	// Limit drives max_chars / min_chars. Must be > 0; refused for the others.
	Limit int `json:"limit,omitempty"`
	// Note is the author's one-line rationale, carried into score_detail so
	// the grade page explains WHY the assertion exists without a round trip
	// to the item editor.
	Note string `json:"note,omitempty"`
}

// parseChecks decodes the stored JSONB. The write gate guarantees the shape;
// this stays defensive anyway because the column outlives the process that
// wrote it.
func parseChecks(raw []byte) ([]Check, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var checks []Check
	if err := json.Unmarshal(raw, &checks); err != nil {
		return nil, fmt.Errorf("decode rubric_checks: %w", err)
	}
	return checks, nil
}

// ChecksValidation is the write gate's verdict on one rubric_checks array.
type ChecksValidation struct {
	Reason string
}

// OK reports whether the array may be stored.
func (v ChecksValidation) OK() bool { return v.Reason == "" }

// ValidateChecks gates one item's serialized assertion set.
//
// An empty payload (NULL, empty JSON text, or `[]`) is legal and means "this
// item is not scored" — the same "no answer key yet is a normal state" rule
// the free-text rubric follows. Everything else must be a bounded array of
// well-formed, uniquely-identified, kind-consistent checks whose phrases carry
// no production-entity references.
func ValidateChecks(raw []byte) ChecksValidation {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return ChecksValidation{}
	}
	if len(raw) > MaxChecksBytes {
		return ChecksValidation{Reason: fmt.Sprintf("rubric_checks exceeds %d bytes", MaxChecksBytes)}
	}
	var checks []Check
	if err := json.Unmarshal(raw, &checks); err != nil {
		return ChecksValidation{Reason: "rubric_checks is not a valid JSON array of checks"}
	}
	if len(checks) == 0 {
		return ChecksValidation{}
	}
	if len(checks) > MaxChecks {
		return ChecksValidation{Reason: fmt.Sprintf("rubric_checks exceeds %d assertions", MaxChecks)}
	}
	seen := make(map[string]bool, len(checks))
	for i, c := range checks {
		where := fmt.Sprintf("rubric_checks[%d]", i)
		if c.ID == "" {
			return ChecksValidation{Reason: where + ": id must not be empty"}
		}
		if seen[c.ID] {
			return ChecksValidation{Reason: where + ": duplicate id " + c.ID}
		}
		seen[c.ID] = true
		if !checkKinds[c.Kind] {
			return ChecksValidation{Reason: where + ": unknown kind " + c.Kind}
		}
		if c.Weight < 0 || c.Weight > MaxCheckWeight {
			return ChecksValidation{Reason: where + ": weight must be between 0 and 100"}
		}
		if c.Note != "" && len(c.Note) > 500 {
			return ChecksValidation{Reason: where + ": note exceeds 500 bytes"}
		}
		switch c.Kind {
		case CheckIncludesAll, CheckIncludesAny, CheckExcludes:
			if len(c.Phrases) == 0 || len(nonEmpty(c.Phrases)) == 0 {
				return ChecksValidation{Reason: where + ": kind " + c.Kind + " needs at least one phrase"}
			}
			if len(c.Phrases) > MaxPhrases {
				return ChecksValidation{Reason: where + fmt.Sprintf(": more than %d phrases", MaxPhrases)}
			}
			for _, p := range nonEmpty(c.Phrases) {
				if len(p) > 500 {
					return ChecksValidation{Reason: where + ": a phrase exceeds 500 bytes"}
				}
				// The body gate's matrix owns which shapes are references;
				// reusing it here keeps one definition of "names a
				// production row" across all three free-text-ish surfaces.
				if v := Validate(p); !v.OK() && !v.Empty && !v.TooLong {
					return ChecksValidation{Reason: where + ": phrase must not reference production entities (" + v.Reason() + ")"}
				}
			}
		case CheckRegex:
			if c.Pattern == "" {
				return ChecksValidation{Reason: where + ": kind regex needs a pattern"}
			}
			if len(c.Pattern) > 1000 {
				return ChecksValidation{Reason: where + ": pattern exceeds 1000 bytes"}
			}
			if _, err := regexp.Compile(c.Pattern); err != nil {
				return ChecksValidation{Reason: where + ": pattern does not compile: " + err.Error()}
			}
		case CheckMaxChars, CheckMinChars:
			if c.Limit <= 0 {
				return ChecksValidation{Reason: where + ": kind " + c.Kind + " needs a positive limit"}
			}
		}
	}
	return ChecksValidation{}
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// CheckVerdict is one assertion's outcome, stored in
// prompt_quiz_result.score_detail. Evidence names the assertion and the
// finding — it never quotes the answer text, so the detail column cannot
// become a second transcript (the answer lives in task_message, joined via
// task_id).
type CheckVerdict struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Weight   float64 `json:"weight"`
	Passed   bool    `json:"passed"`
	Evidence string  `json:"evidence"`
	Note     string  `json:"note,omitempty"`
}

// GradeResult is the outcome of grading one answer against one check set.
//
// Graded=false always means "no grade exists" — no checks, or no answer — and
// the reader must render that as "not graded", never as 0. This is the same
// discipline migration 929 applied to run_tokens (NULL means "not measured")
// and migration 933 applied to outcome (no value is a grade).
type GradeResult struct {
	Graded bool
	// Score is the weighted pass ratio, 0..1. Meaningful only when Graded.
	Score float64
	// Detail carries one verdict per check, in the order the checks are
	// stored, so the UI and the stored JSON stay stable across re-grades.
	Detail []CheckVerdict
}

// Grade evaluates one answer against one item's checks.
//
// The checks arrive as stored JSONB (nil for a check-less item) and the
// answer as the run's final text. Matching is case-insensitive for the phrase
// kinds because an agent's capitalisation is style, not behaviour; the regex
// kind stays case-sensitive so an author who needs case sensitivity can have
// it with an explicit flag.
//
// A checks array that fails to parse or fails ValidateChecks yields
// Graded=false rather than an error: the collection tick must not fail — and
// the sample must not silently shrink — because one item's stored key rotted.
// The write gate makes that path unreachable through the API; the defensive
// branch exists for rows written by other means.
func Grade(checksJSON []byte, answer string) GradeResult {
	checks, err := parseChecks(checksJSON)
	if err != nil {
		return GradeResult{}
	}
	if v := ValidateChecks(mustJSON(checks)); !v.OK() {
		return GradeResult{}
	}
	if len(checks) == 0 {
		return GradeResult{}
	}
	if strings.TrimSpace(answer) == "" {
		// An errored or empty-handed run measured nothing about the prompt.
		// Storing a 0 here would let an outage read as a behavioural
		// regression — the same reason errored rows stay out of the token
		// sample.
		return GradeResult{}
	}

	detail := make([]CheckVerdict, 0, len(checks))
	total, earned := 0.0, 0.0
	for _, c := range checks {
		passed, evidence := evaluate(c, answer)
		w := c.Weight
		if w == 0 {
			w = DefaultCheckWeight
		}
		total += w
		if passed {
			earned += w
		}
		detail = append(detail, CheckVerdict{
			ID:       c.ID,
			Kind:     c.Kind,
			Weight:   w,
			Passed:   passed,
			Evidence: evidence,
			Note:     c.Note,
		})
	}
	return GradeResult{
		Graded: true,
		Score:  earned / total,
		Detail: detail,
	}
}

// evaluate decides one assertion. Each branch's evidence names what was (not)
// found — a phrase, a match, a length — and nothing else.
func evaluate(c Check, answer string) (bool, string) {
	switch c.Kind {
	case CheckIncludesAll:
		var missing []string
		for _, p := range nonEmpty(c.Phrases) {
			if !strings.Contains(strings.ToLower(answer), strings.ToLower(p)) {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			return false, "missing: " + strings.Join(missing, "; ")
		}
		return true, fmt.Sprintf("all %d phrases present", len(nonEmpty(c.Phrases)))
	case CheckIncludesAny:
		for _, p := range nonEmpty(c.Phrases) {
			if strings.Contains(strings.ToLower(answer), strings.ToLower(p)) {
				return true, "matched phrase present"
			}
		}
		return false, fmt.Sprintf("none of the %d accepted phrases present", len(nonEmpty(c.Phrases)))
	case CheckExcludes:
		var found []string
		for _, p := range nonEmpty(c.Phrases) {
			if strings.Contains(strings.ToLower(answer), strings.ToLower(p)) {
				found = append(found, p)
			}
		}
		if len(found) > 0 {
			return false, "forbidden phrase present: " + strings.Join(found, "; ")
		}
		return true, fmt.Sprintf("none of the %d forbidden phrases present", len(nonEmpty(c.Phrases)))
	case CheckRegex:
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			// Unreachable through the write gate; a stored-but-broken pattern
			// fails the check with the reason rather than panicking.
			return false, "pattern does not compile"
		}
		if re.MatchString(answer) {
			return true, "pattern matched"
		}
		return false, "pattern not matched"
	case CheckMaxChars:
		n := len([]rune(answer))
		if n > c.Limit {
			return false, fmt.Sprintf("length %d exceeds limit %d", n, c.Limit)
		}
		return true, fmt.Sprintf("length %d within limit %d", n, c.Limit)
	case CheckMinChars:
		n := len([]rune(answer))
		if n < c.Limit {
			return false, fmt.Sprintf("length %d below limit %d", n, c.Limit)
		}
		return true, fmt.Sprintf("length %d meets limit %d", n, c.Limit)
	}
	// Unknown kinds are refused at write time; a stored one fails closed.
	return false, "unknown kind"
}

// mustJSON marshals checks for re-validation inside Grade. marshal of
// []Check cannot fail (no channels, no funcs), so the panic path is
// unreachable; the helper keeps Grade's signature free of an error that has
// nowhere to go.
func mustJSON(checks []Check) []byte {
	raw, err := json.Marshal(checks)
	if err != nil {
		return nil
	}
	return raw
}
