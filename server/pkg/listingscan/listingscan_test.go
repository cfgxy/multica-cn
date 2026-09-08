package listingscan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScanCleanListingPasses(t *testing.T) {
	res := Scan(Input{
		Name:        "team-notes",
		Summary:     "Shared meeting notes helper.",
		Description: "Reads the team notes directory and summarises the last standup.",
		HomepageURL: "https://example.invalid/notes",
		Categories:  []string{"documents", "productivity"},
		SourceURL:   "https://github.com/example/notes-skill",
	})
	if !res.OK() {
		t.Fatalf("clean listing should pass, got %v", res.Findings)
	}
	if res.Revision != Revision {
		t.Fatalf("revision = %q, want %q", res.Revision, Revision)
	}
}

func TestScanBlocksCredentialInFreeText(t *testing.T) {
	res := Scan(Input{
		Name:        "leaky",
		Description: "line one\nexport GITHUB_TOKEN=ghp_" + strings.Repeat("a", 36),
	})
	if res.OK() {
		t.Fatal("a pasted token in the description must block publication")
	}
	for _, f := range res.Findings {
		if f.Field != "description" {
			t.Errorf("finding %+v should point at description", f)
		}
		if f.Line != 2 {
			t.Errorf("finding %+v should point at line 2", f)
		}
		if f.Mask != Mask {
			t.Errorf("finding %+v must carry the fixed mask", f)
		}
	}
}

// The whole point of the package: a finding must be serialisable into a 422
// body and a log line without carrying the matched text.
func TestFindingNeverCarriesMatchedText(t *testing.T) {
	const secret = "ghp_" + "0123456789012345678901234567890123456"
	res := Scan(Input{Name: "x", Description: secret})
	if res.OK() {
		t.Fatal("expected the token to be detected")
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("serialised scan result echoed the matched secret")
	}
	for _, f := range res.Findings {
		if strings.Contains(f.Message(), secret) {
			t.Fatal("Finding.Message echoed the matched secret")
		}
	}
}

func TestScanAcceptsDeclaredPlaceholderInCredentialField(t *testing.T) {
	res := Scan(Input{
		Name: "remote-api",
		ConfigTemplate: json.RawMessage(`{
			"type": "http",
			"url": "https://api.example.invalid/mcp",
			"headers": {"Authorization": "${api_token}"}
		}`),
		DeclaredPlaceholders: []string{"api_token"},
	})
	if !res.OK() {
		t.Fatalf("a declared placeholder in a credential header must pass, got %v", res.Findings)
	}
}

func TestScanBlocksLiteralInCredentialField(t *testing.T) {
	// Deliberately a value no shape detector recognises: a short internal
	// token. The credential-positioned rule is what has to catch it.
	res := Scan(Input{
		Name: "remote-api",
		ConfigTemplate: json.RawMessage(`{
			"type": "http",
			"url": "https://api.example.invalid/mcp",
			"headers": {"Authorization": "hunter2"}
		}`),
	})
	if res.OK() {
		t.Fatal("a literal value under a credential-named key must block")
	}
	found := false
	for _, f := range res.Findings {
		if f.Rule == "credential_field_literal" && f.Field == "config_template.headers.Authorization" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected credential_field_literal on the Authorization header, got %v", res.Findings)
	}
}

func TestScanBlocksUndeclaredPlaceholder(t *testing.T) {
	// An unregistered `${key}` has no input field in the install dialog, so
	// RenderMarketplaceMcpConfig would refuse at install time. Catching it at
	// publish time is the difference between the publisher fixing it and every
	// installer hitting a dead listing.
	res := Scan(Input{
		Name:                 "remote-api",
		ConfigTemplate:       json.RawMessage(`{"type":"http","url":"https://x.invalid/${region}"}`),
		DeclaredPlaceholders: []string{"api_token"},
	})
	if res.OK() {
		t.Fatal("an undeclared placeholder must block")
	}
	if res.Findings[0].Rule != "undeclared_placeholder" {
		t.Fatalf("got %v, want undeclared_placeholder", res.Findings)
	}
}

func TestScanWalksNestedTemplateStructures(t *testing.T) {
	res := Scan(Input{
		Name: "stdio-server",
		ConfigTemplate: json.RawMessage(`{
			"command": "npx",
			"args": ["-y", "server", "--key", "AKIAIOSFODNN7EXAMPLE"],
			"env": {"HOME": "/tmp"}
		}`),
	})
	if res.OK() {
		t.Fatal("an AWS key inside an args array must block")
	}
	if got := res.Findings[0].Field; got != "config_template.args[3]" {
		t.Fatalf("field = %q, want config_template.args[3]", got)
	}
}

func TestScanIgnoresNonCredentialLiterals(t *testing.T) {
	res := Scan(Input{
		Name: "filesystem",
		ConfigTemplate: json.RawMessage(`{
			"command": "npx",
			"args": ["-y", "@modelcontextprotocol/server-filesystem", "${root_path}"]
		}`),
		DeclaredPlaceholders: []string{"root_path"},
	})
	if !res.OK() {
		t.Fatalf("an ordinary stdio template must pass, got %v", res.Findings)
	}
}

func TestScanTruncatesRatherThanReturningAWall(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxFindings+20; i++ {
		b.WriteString("PASSWORD=value\n")
	}
	res := Scan(Input{Name: "x", Description: b.String()})
	if !res.Truncated {
		t.Fatal("expected truncation past the cap")
	}
	if len(res.Findings) > maxFindings {
		t.Fatalf("findings = %d, want <= %d", len(res.Findings), maxFindings)
	}
	if res.OK() {
		t.Fatal("truncation must not turn into a pass")
	}
}

func TestScanIsStableAcrossRuns(t *testing.T) {
	in := Input{
		Name:        "x",
		Description: "PASSWORD=a\nBearer abcdefghijkl\ncontact: nobody@example.invalid",
	}
	first, err := json.Marshal(Scan(in))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Scan(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("two scans of identical content produced different results")
	}
}

func TestScanReportsUnparseableTemplate(t *testing.T) {
	res := Scan(Input{Name: "x", ConfigTemplate: json.RawMessage(`{"command":`)})
	if res.OK() {
		t.Fatal("an unparseable template must not pass the gate")
	}
	if res.Findings[0].Rule != "config_template_unparseable" {
		t.Fatalf("got %v", res.Findings)
	}
}
