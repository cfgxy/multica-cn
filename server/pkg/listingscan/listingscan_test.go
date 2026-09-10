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
		Placeholders: []Placeholder{{Key: "api_token"}},
	})
	if !res.OK() {
		t.Fatalf("a declared placeholder in a credential header must pass, got %v", res.Findings)
	}
}

func TestScanBlocksLiteralInCredentialField(t *testing.T) {
	// Deliberately a value no shape detector recognises: a short internal
	// token. The value-container rule is what has to catch it.
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
		if f.Rule == "container_literal_value" && f.Field == "config_template.headers[0]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected container_literal_value on the Authorization header, got %v", res.Findings)
	}
}

func TestScanBlocksUndeclaredPlaceholder(t *testing.T) {
	// An unregistered `${key}` has no input field in the install dialog, so
	// RenderMarketplaceMcpConfig would refuse at install time. Catching it at
	// publish time is the difference between the publisher fixing it and every
	// installer hitting a dead listing.
	res := Scan(Input{
		Name:           "remote-api",
		ConfigTemplate: json.RawMessage(`{"type":"http","url":"https://x.invalid/${region}"}`),
		Placeholders:   []Placeholder{{Key: "api_token"}},
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
		Placeholders: []Placeholder{{Key: "root_path"}},
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

// A credential parked in a placeholder's label or description reaches the
// catalog exactly like one in the description field, so it must block. Before
// RUYI-99's rework the handler only handed the scanner the placeholder KEYS,
// which made this the shortest path to a public secret.
func TestScanBlocksCredentialInPlaceholderMetadata(t *testing.T) {
	const token = "ghp_" + "0123456789012345678901234567890123456"
	for _, tc := range []struct {
		name  string
		in    Placeholder
		field string
	}{
		{"label", Placeholder{Key: "api_token", Label: token}, "placeholders[0].label"},
		{"description", Placeholder{Key: "api_token", Description: "use " + token}, "placeholders[0].description"},
		{"key", Placeholder{Key: token}, "placeholders[0].key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(Input{Name: "remote-api", Placeholders: []Placeholder{tc.in}})
			if res.OK() {
				t.Fatalf("a credential in placeholder %s must block", tc.name)
			}
			found := false
			for _, f := range res.Findings {
				if f.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a finding on %s, got %v", tc.field, res.Findings)
			}
			encoded, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), token) {
				t.Fatal("serialised result echoed the placeholder secret")
			}
		})
	}
}

// A publisher-supplied JSON key is publisher text: it must be scanned, and it
// must never be echoed back as the finding's field. The field previously
// carried `config_template.headers.<key>` verbatim, so a 422 body handed the
// key straight back.
func TestFindingFieldNeverEchoesPublisherKey(t *testing.T) {
	const token = "ghp_" + "0123456789012345678901234567890123456"
	res := Scan(Input{
		Name:           "remote-api",
		ConfigTemplate: json.RawMessage(`{"type":"http","url":"https://x.invalid/mcp","headers":{"X-` + token + `":"${api_token}"}}`),
		Placeholders:   []Placeholder{{Key: "api_token"}},
	})
	if res.OK() {
		t.Fatal("a credential-shaped header NAME must block")
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatalf("finding echoed the publisher-supplied key: %s", encoded)
	}
	for _, f := range res.Findings {
		if strings.Contains(f.Message(), token) {
			t.Fatal("Finding.Message echoed the publisher-supplied key")
		}
	}
}

// Every non-empty value in `headers`/`env` must be a whole registered
// placeholder. The old rule only held keys that LOOKED like credentials to that
// standard, so a literal under an innocuous name published freely.
func TestScanBlocksLiteralUnderInnocuousContainerKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		field    string
	}{
		{
			"header",
			`{"type":"http","url":"https://x.invalid/mcp","headers":{"X-Team":"acme-internal"}}`,
			"config_template.headers[0]",
		},
		{
			"env",
			`{"command":"npx","args":["-y","server"],"env":{"REGION_SEED":"abc123"}}`,
			"config_template.env[0]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Scan(Input{Name: "x", ConfigTemplate: json.RawMessage(tc.template)})
			if res.OK() {
				t.Fatalf("a literal value in %s must block", tc.name)
			}
			found := false
			for _, f := range res.Findings {
				if f.Rule == "container_literal_value" && f.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected container_literal_value on %s, got %v", tc.field, res.Findings)
			}
		})
	}
}

// The counterpart: registered placeholders everywhere in the containers pass,
// and an empty value is not a literal.
func TestScanAcceptsFullyPlaceholderedContainers(t *testing.T) {
	res := Scan(Input{
		Name: "x",
		ConfigTemplate: json.RawMessage(`{
			"type": "http",
			"url": "https://x.invalid/mcp",
			"headers": {"X-Team": "${team}", "Authorization": "${api_token}"},
			"env": {"OPTIONAL": ""}
		}`),
		Placeholders: []Placeholder{{Key: "team"}, {Key: "api_token"}},
	})
	if !res.OK() {
		t.Fatalf("fully placeholdered containers must pass, got %v", res.Findings)
	}
}

// Map positions come from a sorted key order, so two scans of the same content
// address the same leaf the same way.
func TestContainerPositionsAreStable(t *testing.T) {
	in := Input{
		Name:           "x",
		ConfigTemplate: json.RawMessage(`{"type":"http","url":"https://x.invalid","headers":{"Z-One":"a","A-Two":"b","M-Three":"c"}}`),
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
		t.Fatal("map positions differed between two scans of identical content")
	}
	res := Scan(in)
	// Sorted: A-Two, M-Three, Z-One.
	want := map[string]bool{
		"config_template.headers[0]": true,
		"config_template.headers[1]": true,
		"config_template.headers[2]": true,
	}
	for _, f := range res.Findings {
		if f.Rule == "container_literal_value" && !want[f.Field] {
			t.Fatalf("unexpected field %q", f.Field)
		}
	}
}
