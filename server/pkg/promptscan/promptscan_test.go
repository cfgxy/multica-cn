package promptscan

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fake credentials below are shaped to match the detectors and are not real.

func TestScanCleanPromptPasses(t *testing.T) {
	res := Scan("You are a careful reviewer.\nAlways cite file and line.\n")
	if !res.OK() {
		t.Fatalf("clean prompt should pass, got findings: %+v", res.Findings)
	}
	if res.Revision == "" {
		t.Fatal("revision must travel with every result so a pass is re-checkable")
	}
}

func TestScanReportsCategoryRuleAndLine(t *testing.T) {
	content := strings.Join([]string{
		"line one is fine",
		"line two is fine",
		"use sk-abcdefghijklmnopqrstuvwxyz012345 to call the API",
	}, "\n")

	res := Scan(content)
	if res.OK() {
		t.Fatal("api key must be detected")
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.Category != CategoryAPIKey {
		t.Errorf("category = %q, want %q", f.Category, CategoryAPIKey)
	}
	if f.Rule == "" {
		t.Error("rule must be set so a report identifies which detector fired")
	}
	if f.Line != 3 {
		t.Errorf("line = %d, want 3", f.Line)
	}
}

// The whole reason this package exists rather than reusing pkg/redact: a
// finding must never carry any of the matched text. The mask is a fixed
// literal, so no prefix of the secret survives into scan_result, the 422 body
// or a log line.
func TestFindingNeverCarriesRawSecret(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	res := Scan("token: " + secret)
	if res.OK() {
		t.Fatal("github token must be detected")
	}
	for _, f := range res.Findings {
		if f.Mask != Mask {
			t.Errorf("mask = %q, want the fixed literal %q", f.Mask, Mask)
		}
		if strings.Contains(f.Message(), secret) {
			t.Fatal("message leaked the full secret")
		}
		// Even a 4-character prefix is content: assert none of the secret's
		// leading runes made it out.
		if strings.Contains(f.Message(), secret[:4]) {
			t.Fatalf("message %q leaked a prefix of the secret", f.Message())
		}
	}
}

// The whole Result — the exact bytes that land in scan_result and in a 422 —
// must be free of the input's secret material.
func TestMarshalledResultCarriesNoSecretMaterial(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	res := Scan("PASSWORD=hunter2hunter2\ntoken: " + secret)
	if res.OK() {
		t.Fatal("content must be detected")
	}
	blob, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{secret, secret[:6], "hunter2"} {
		if strings.Contains(string(blob), leak) {
			t.Fatalf("serialised result leaked %q: %s", leak, blob)
		}
	}
}

func TestScanCoversRequiredCategories(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    Category
	}{
		{"openai key", "sk-abcdefghijklmnopqrstuvwxyz012345", CategoryAPIKey},
		{"github token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", CategoryToken},
		{"slack token", "xoxb-1234567890-abcdefghijkl", CategoryToken},
		{"aws key id", "AKIAIOSFODNN7EXAMPLE", CategoryAPIKey},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk", CategoryToken},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----", CategoryPrivateKey},
		{"password assignment", "PASSWORD=hunter2hunter2", CategoryPassword},
		{"connection string", "postgres://user:s3cretpw@db.example.com:5432/app", CategoryPassword},
		{"cookie header", "Cookie: session_id=abcdef1234567890abcdef", CategoryCookie},
		{"email pii", "contact alice.smith@example.com for access", CategoryPII},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s must be detected", tc.name)
			}
			var got []Category
			for _, f := range res.Findings {
				got = append(got, f.Category)
				if f.Category == tc.want {
					return
				}
			}
			t.Errorf("category = %v, want %v", got, tc.want)
		})
	}
}

// Fail-closed: a finding blocks. There is no confidence threshold, no "mark as
// false positive" and no severity that a caller may choose to ignore, so the
// only shape the API offers is OK() == false.
func TestScanIsFailClosed(t *testing.T) {
	res := Scan("PASSWORD=hunter2hunter2")
	if res.OK() {
		t.Fatal("any finding must block")
	}
}

// Findings are deduplicated per (rule, line) so a prompt that repeats one
// credential does not produce an unbounded report, but distinct lines stay
// distinct — a publisher has to be able to find every occurrence.
func TestFindingsDeduplicatePerRuleAndLine(t *testing.T) {
	line := "sk-abcdefghijklmnopqrstuvwxyz012345 sk-abcdefghijklmnopqrstuvwxyz012345"
	res := Scan(line + "\n" + line)
	if len(res.Findings) != 2 {
		t.Fatalf("want one finding per line, got %d: %+v", len(res.Findings), res.Findings)
	}
	if res.Findings[0].Line != 1 || res.Findings[1].Line != 2 {
		t.Errorf("lines = %d,%d want 1,2", res.Findings[0].Line, res.Findings[1].Line)
	}
}

// A prompt is user text of unbounded size; the scan runs synchronously on the
// publish path, so it must not be quadratic or unbounded in report size.
func TestScanBoundsFindingCount(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxFindings*3; i++ {
		b.WriteString("PASSWORD=hunter2hunter2\n")
	}
	res := Scan(b.String())
	if len(res.Findings) > maxFindings {
		t.Fatalf("findings = %d, want <= %d", len(res.Findings), maxFindings)
	}
	if res.OK() {
		t.Fatal("truncated report still blocks")
	}
	if !res.Truncated {
		t.Error("truncation must be visible to the caller")
	}
}

// A short match must not be echoed either — the fixed mask makes match length
// irrelevant, which is exactly the property being pinned here.
func TestShortMatchIsNotEchoed(t *testing.T) {
	res := Scan("Cookie: a=bc")
	if res.OK() {
		t.Fatal("cookie header must be detected")
	}
	for _, f := range res.Findings {
		if f.Mask != Mask {
			t.Errorf("mask = %q, want %q", f.Mask, Mask)
		}
	}
}
