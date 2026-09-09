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

// A credential is a credential whatever punctuation surrounds it. The original
// rule required `=` or `:` IMMEDIATELY after the field name, so every JSON form
// — where the key's closing quote sits in between — passed the gate and the
// value was frozen into the cross-workspace catalog.
func TestScanDetectsCredentialsInEveryCommonFormat(t *testing.T) {
	const secret = "s3cr3t-value-not-real"
	cases := []struct {
		name    string
		content string
	}{
		{"json", `{"password":"` + secret + `"}`},
		{"json spaced", `{ "token" : "` + secret + `" }`},
		{"json api key", `{"api_key": "` + secret + `"}`},
		{"json nested", `{"db": {"db_password": "` + secret + `"}}`},
		{"yaml", "database:\n  password: " + secret},
		{"yaml quoted", `client_secret: "` + secret + `"`},
		{"yaml hyphenated", "auth-token: " + secret},
		{"env file", "ACCESS_TOKEN=" + secret},
		{"shell export", "export DB_PASSWORD=" + secret},
		{"http header", "Authorization: " + secret},
		{"custom header", "X-Api-Key: " + secret},
		{"cookie header json", `{"cookie": "session=` + secret + `"}`},
		{"go map literal", `"private_key": "` + secret + `",`},
		{"ruby hashrocket", `"refresh_token" => "` + secret + `"`},
		{"colon equals", `passphrase := "` + secret + `"`},
		{"camel case", `{"clientSecret": "` + secret + `"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s form must be detected, content %q passed the gate", tc.name, tc.content)
			}
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), secret) {
				t.Fatalf("serialised result leaked the value: %s", blob)
			}
		})
	}
}

// The counterweight to the case above: a rule broad enough to catch every
// format is broad enough to block prose that merely discusses credentials, and
// a publisher who cannot publish "ask the user for their password" would work
// around the gate rather than through it.
func TestScanDoesNotBlockCredentialProse(t *testing.T) {
	cases := []string{
		"Ask the user for their password before continuing.",
		"Never log the API key you were given.",
		"The token is stored in the vault; do not read it.",
		`If the request fails with 401, the auth token expired.`,
		// A key with no value is a schema, not a secret.
		`{"password": ""}`,
		"password:",
	}
	for _, content := range cases {
		if res := Scan(content); !res.OK() {
			t.Errorf("prose %q must not block, got %+v", content, res.Findings)
		}
	}
}

// `$`, `<` and `{` open a placeholder, but they also open plenty of real
// secrets. Releasing on the first character alone means `password: $ecret123`
// publishes; only syntax that is actually CLOSED is a template reference.
func TestScanBlocksIncompletePlaceholders(t *testing.T) {
	cases := []struct {
		name    string
		content string
		secret  string
	}{
		{"dollar prefix", "password: $ecret123-not-real", "$ecret123-not-real"},
		{"angle prefix", "token: <s3cr3t-value-not-real", "<s3cr3t-value-not-real"},
		{"brace prefix", "api_key: {s3cr3t-value-not-real", "{s3cr3t-value-not-real"},
		{"unclosed dollar brace", `client_secret: "${s3cr3t-value-not-real"`, "s3cr3t-value-not-real"},
		{"placeholder with trailing secret", "password: ${A}s3cr3t-value-not-real", "s3cr3t-value-not-real"},
		// A closed placeholder in ONE field must not release the real value in
		// the next: the rest-of-line the placeholder test reads spans both.
		{"placeholder beside a real secret", `{"password": "<redacted>", "token": "s3cr3t-value-not-real"}`, "s3cr3t-value-not-real"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s must be detected, content %q passed the gate", tc.name, tc.content)
			}
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), tc.secret) {
				t.Fatalf("serialised result leaked the value: %s", blob)
			}
		})
	}
}

// A field the validator RELEASES must not consume the rest of its line. Go's
// FindAll only resumes after the end of the previous match, so a released match
// that reached the line ending took every later field on that line with it and
// the real credential beside a schema field published.
//
// Each case pins both halves: the leading field alone must still publish, and
// the same line with a real credential appended must block. The first assertion
// is what makes the second meaningful — without it, a finding for the leading
// field would satisfy the test while the credential still went unscanned.
func TestReleasedFieldDoesNotSwallowLaterCredentialOnSameLine(t *testing.T) {
	const secret = "s3cr3t-value-not-real"
	cases := []struct {
		name     string
		released string // must publish on its own
		full     string // the same line, with a real credential after it
	}{
		{"unquoted null then token", `{"password":null}`, `{"password":null,"token":"` + secret + `"}`},
		{"empty string then token", `{"password":""}`, `{"password":"","token":"` + secret + `"}`},
		{"placeholder then token", `{"password":"<redacted>"}`, `{"password":"<redacted>","token":"` + secret + `"}`},
		{"dollar placeholder then api key", `{"password":"${DB_PASSWORD}"}`, `{"password":"${DB_PASSWORD}","api_key":"` + secret + `"}`},
		{"unquoted false then password", `{"token":false}`, `{"token":false,"db_password":"` + secret + `"}`},
		{"env placeholder then env secret", `DB_PASSWORD=${DB_PASSWORD}`, `DB_PASSWORD=${DB_PASSWORD} ACCESS_TOKEN=` + secret},
		{"cookie schema then token", `{"cookie":""}`, `{"cookie":"","token":"` + secret + `"}`},
		{"placeholder then non-credential then key", `{"password":"<x>","a":1}`, `{"password":"<x>","a":1,"api_key":"` + secret + `"}`},
		{"unquoted yaml placeholder then token", `password: ${A}`, `password: ${A} token: ` + secret},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if res := Scan(tc.released); !res.OK() {
				t.Fatalf("leading field %q must still publish, got %+v", tc.released, res.Findings)
			}
			res := Scan(tc.full)
			if res.OK() {
				t.Fatalf("credential after a released field must block, content %q passed the gate", tc.full)
			}
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), secret) {
				t.Fatalf("serialised result leaked the value: %s", blob)
			}
		})
	}
}

// `null` unquoted is the absence of a value; `"null"` quoted is a five-character
// string that happens to spell it. Only the first is a schema, and treating both
// alike lets any secret publish by being named after a keyword.
func TestScanBlocksQuotedNotASecretKeywords(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"json quoted null", `{"password":"null"}`},
		{"json quoted undefined", `{"token":"undefined"}`},
		{"yaml quoted false", `password: "false"`},
		{"yaml single quoted none", `client_secret: 'none'`},
		{"escaped json quoted nil", `{\"api_key\":\"nil\"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s must be detected, content %q passed the gate", tc.name, tc.content)
			}
			for _, f := range res.Findings {
				if f.Mask != Mask {
					t.Fatalf("finding carried something other than the fixed mask: %+v", f)
				}
			}
		})
	}
}

// The counterweight to both tests above: the shapes a publishable prompt is
// meant to carry must still publish, or the gate becomes something publishers
// route around rather than through.
func TestScanReleasesCompletePlaceholdersAndUnquotedKeywords(t *testing.T) {
	cases := []string{
		"password: ${DB_PASSWORD}",
		`{"password": "${DB_PASSWORD}"}`,
		"api_key: {{ secret }}",
		"token: <your-token-here>",
		`{"client_secret": "<replace-me>"}`,
		`{"password": null}`,
		"token: undefined",
		"password: false",
		"api_key: ~",
		// A released value followed by an ordinary field on the same line: the
		// value boundary, not the line ending, is what the placeholder test reads.
		`{"password": "${DB_PASSWORD}", "other": 1}`,
		`{"password": null, "other": 1}`,
		"api_key: {{ secret }}\nname: demo",
		"password: ${DB_PASSWORD} # from env",
	}
	for _, content := range cases {
		if res := Scan(content); !res.OK() {
			t.Errorf("content %q must not block, got %+v", content, res.Findings)
		}
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

// A JSON encoder is free to put the value on its own line, and pretty-printers
// routinely do it for long values. Scanning line by line meant the key line
// ("password":) had no value and the value line had no key, so neither
// half matched and a real credential reached the public catalog through the
// one formatting choice a publisher is least likely to think about.
func TestScanDetectsCredentialsSplitAcrossLines(t *testing.T) {
	const secret = "s3cr3t-value-not-real"
	cases := []struct {
		name    string
		content string
	}{
		{"json value on next line", "{\n  \"password\":\n    \"" + secret + "\"\n}"},
		{"json value on next line unquoted key", "{\n  password:\n    \"" + secret + "\"\n}"},
		{"json separator on next line", "{\n  \"api_key\"\n  : \"" + secret + "\"\n}"},
		{"yaml value on next line", "database:\n  password:\n    " + secret},
		{"yaml block scalar", "client_secret: >\n  " + secret},
		{"env continuation", "ACCESS_TOKEN=\\\n" + secret},
		{"crlf line ending", "{\r\n  \"db_password\":\r\n    \"" + secret + "\"\r\n}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s must be detected, content %q passed the gate", tc.name, tc.content)
			}
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), secret) {
				t.Fatalf("serialised result leaked the value: %s", blob)
			}
		})
	}
}

// JSON that has itself been embedded in a JSON string — a webhook body pasted
// into a prompt, a log line, an escaped example — carries backslashes before
// every quote. Those backslashes sat between the field name and the separator,
// so the assignment rule did not match and the credential published.
func TestScanDetectsCredentialsInEscapedJSON(t *testing.T) {
	const secret = "s3cr3t-value-not-real"
	cases := []struct {
		name    string
		content string
	}{
		{"escaped json", `{\"password\":\"` + secret + `\"}`},
		{"escaped json spaced", `{\"api_key\": \"` + secret + `\"}`},
		{"escaped json in prose", `The body was {\"access_token\":\"` + secret + `\"} when it failed.`},
		{"double escaped", `{\\"client_secret\\":\\"` + secret + `\\"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(tc.content)
			if res.OK() {
				t.Fatalf("%s must be detected, content %q passed the gate", tc.name, tc.content)
			}
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), secret) {
				t.Fatalf("serialised result leaked the value: %s", blob)
			}
		})
	}
}

// The counterweight to both cases above. Looking past a line ending for the
// value is exactly what makes an empty schema field look like an assignment:
// the next line always has SOMETHING on it. These are the shapes that must
// still publish, or the gate becomes something publishers route around.
func TestScanDoesNotBlockMultilineProseOrEmptyValues(t *testing.T) {
	cases := []string{
		// A key with no value, with the closing brace on the next line.
		"{\n  \"password\": \"\"\n}",
		"{\n  \"password\": null\n}",
		"{\n  \"api_key\": \"\",\n  \"other\": 1\n}",
		// A schema or template, not a filled-in config.
		"{\n  \"password\":\n}",
		"password:\n",
		"password:\n\n",
		// Prose that happens to end a line on the word.
		"Ask the user for their password\nbefore continuing.",
		"Never log the API key\nyou were given.",
		// A YAML key whose next line is another key, not a value.
		"database:\n  password:\n  host: localhost",
	}
	for _, content := range cases {
		if res := Scan(content); !res.OK() {
			t.Errorf("content %q must not block, got %+v", content, res.Findings)
		}
	}
}
