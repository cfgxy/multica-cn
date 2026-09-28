package promptquiz

import (
	"strings"
	"testing"
)

// The gate's contract is a pair: a body that names nothing production-side is
// stored, and one that does is refused with a reason its author can act on.
// Both halves are asserted here because only the pair is the rule — a gate that
// rejected everything would pass a rejection-only suite.

func TestValidateAcceptsIsolatedQuestions(t *testing.T) {
	bodies := []string{
		"Summarise the following three requirements in one sentence each, then list any contradiction between them.",
		"A teammate reports a build failure with no log. What do you ask for first, and why that rather than a fix?",
		"用一句话说明为什么测试失败不应阻断发布，并给出一个反例。",
		// Prose that looks structurally like an id but is not one. A gate that
		// trips on these would make ordinary questions unwritable.
		"Explain the difference between UTF-8 and UTF-16 for a reader who knows neither.",
		"Given a 2026-09-26 deadline and a 3-day estimate, what do you cut?",
	}
	for _, body := range bodies {
		if v := Validate(body); !v.OK() {
			t.Errorf("Validate(%.40q) rejected an isolated body: %s", body, v.Reason())
		}
	}
}

func TestValidateRejectsProductionEntityReferences(t *testing.T) {
	cases := []struct {
		name string
		body string
		kind EntityRefKind
	}{
		{"bare uuid", "Read the context of task 01a0d486-0a24-75b4-9058-580de6a06b9f and summarise it.", RefUUID},
		{"issue key", "What is the acceptance criterion of RUYI-185?", RefIssueKey},
		{"mention link", "Ask [@Leader](mention://agent/3478a149-34cd-49b4-bbc4-1b789005fbc0) to confirm.", RefMention},
		{"url", "Fetch https://example.invalid/spec.json and answer from it.", RefURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Validate(tc.body)
			if v.OK() {
				t.Fatalf("Validate accepted a body naming a production entity: %q", tc.body)
			}
			if len(v.Refs) == 0 || v.Refs[0].Kind != tc.kind {
				t.Fatalf("refs = %+v, want first kind %q", v.Refs, tc.kind)
			}
			// The refusal must locate the reference without echoing it: the body
			// is workspace-private (Owner Q8) and the reason travels into API
			// responses and logs.
			reason := v.Reason()
			if !strings.Contains(reason, string(tc.kind)) {
				t.Fatalf("reason %q does not name the kind that fired", reason)
			}
			for _, secret := range []string{
				"01a0d486-0a24-75b4-9058-580de6a06b9f",
				"RUYI-185",
				"3478a149-34cd-49b4-bbc4-1b789005fbc0",
				"example.invalid",
			} {
				if strings.Contains(reason, secret) {
					t.Fatalf("reason echoed matched text %q: %s", secret, reason)
				}
			}
		})
	}
}

// A mention link embeds a UUID. Reporting both would tell the author to remove
// two things where there is one.
func TestValidateReportsMentionOnceNotAlsoAsUUID(t *testing.T) {
	v := Validate("Ask [@X](mention://agent/3478a149-34cd-49b4-bbc4-1b789005fbc0) about it.")
	if len(v.Refs) != 1 {
		t.Fatalf("refs = %+v, want exactly one (the mention, not also its uuid)", v.Refs)
	}
	if v.Refs[0].Kind != RefMention {
		t.Fatalf("kind = %q, want %q", v.Refs[0].Kind, RefMention)
	}
}

func TestValidateBoundsEmptyAndOversizeBodies(t *testing.T) {
	if v := Validate("   \n\t "); !v.Empty || v.OK() {
		t.Fatalf("whitespace-only body: %+v, want Empty", v)
	}
	if v := Validate(strings.Repeat("a", MaxBodyBytes+1)); !v.TooLong || v.OK() {
		t.Fatalf("oversize body: %+v, want TooLong", v)
	}
	if v := Validate(strings.Repeat("a", MaxBodyBytes)); !v.OK() {
		t.Fatalf("body exactly at the ceiling was rejected: %s", v.Reason())
	}
}
