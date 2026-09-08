package service

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// The scanner's contract is narrow and worth stating: it refuses credential
// FORMATS, it never echoes what it found, and it does not fire on ordinary
// source. Each of those is a separate way the feature can be wrong.

func bundleWithFile(path, content string) plugincontract.Bundle {
	return plugincontract.Bundle{
		Files: []plugincontract.BundleFile{{Path: path, Content: []byte(content)}},
	}
}

func TestPublishRefusesABundleCarryingACredential(t *testing.T) {
	// Every value here is a synthetic string matching the provider's published
	// FORMAT. None of them authenticates anywhere.
	cases := []struct {
		name    string
		content string
		kind    string
	}{
		{"aws", `const id = "AKIAQQQQQQQQQQQQQQQQ";`, "AWS access key ID"},
		{"github", `token: ghp_` + strings.Repeat("a", 36), "GitHub token"},
		{"slack", `const hook = "xoxb-0000000000-abcdefghij";`, "Slack token"},
		{"anthropic", `key = "sk-ant-` + strings.Repeat("b", 24) + `"`, "Anthropic API key"},
		{"openai", `key = "sk-` + strings.Repeat("c", 40) + `"`, "OpenAI API key"},
		{"google", `const k = "AIza` + strings.Repeat("d", 35) + `";`, "Google API key"},
		{"stripe", `sk_live_` + strings.Repeat("e", 24), "Stripe secret key"},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----\nQUJD\n-----END RSA PRIVATE KEY-----", "private key"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K", "JSON Web Token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := scanBundleForSecrets(bundleWithFile("ui/main.js", tc.content))
			if err == nil {
				t.Fatalf("scan accepted a bundle carrying a %s", tc.kind)
			}
			var pluginErr *PluginError
			if !asPluginError(err, &pluginErr) || pluginErr.Kind != PluginErrorInvalid {
				t.Fatalf("kind = %v, want %q so publishing answers 400 rather than 500", err, PluginErrorInvalid)
			}
			message := pluginErr.Error()
			if !strings.Contains(message, tc.kind) {
				t.Fatalf("message does not name the credential kind, so the author cannot tell what to rotate: %q", message)
			}
			if !strings.Contains(message, "ui/main.js") {
				t.Fatalf("message does not name the file: %q", message)
			}
		})
	}
}

// The rejection travels into server logs, an API response and — in practice —
// an issue comment. A scanner that quotes the match turns one leaked secret
// into four copies of it.
func TestTheRejectionNeverEchoesTheSecret(t *testing.T) {
	secret := "AKIAZZZZZZZZZZZZZZZZ"
	err := scanBundleForSecrets(bundleWithFile("server/handler.mjs", "const id = \""+secret+"\";"))
	if err == nil {
		t.Fatal("scan accepted a bundle carrying an AWS key")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the rejection quotes the secret it found: %q", err.Error())
	}
}

// It reports WHERE, because "somewhere in this file" sends an author grepping
// through a bundle they already thought was clean.
func TestTheRejectionNamesTheLine(t *testing.T) {
	content := "// one\n// two\nconst id = \"AKIAYYYYYYYYYYYYYYYY\";\n"
	err := scanBundleForSecrets(bundleWithFile("ui/main.js", content))
	if err == nil {
		t.Fatal("scan accepted a bundle carrying an AWS key")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("message does not point at line 3: %q", err.Error())
	}
}

// A scanner that fires on ordinary plugin source would be turned off, and a
// scanner that is off protects nobody. These are the shapes real bundles carry.
func TestOrdinaryPluginSourcePublishesUntouched(t *testing.T) {
	clean := []struct {
		name    string
		path    string
		content string
	}{
		{"config field reference", "ui/main.js", `const key = ctx.config.api_key; // declared as a secret field`},
		{"placeholder", "README.md", "Set `api_key` to your token, e.g. `sk-...`."},
		{"prose about keys", "README.md", "This plugin needs a private key for signing. Do not commit it."},
		{"base64 asset", "ui/logo.js", "export const logo = \"" + strings.Repeat("QUJDRA", 40) + "\";"},
		{"binary", "ui/font.woff", "\x00\x01AKIAQQQQQQQQQQQQQQQQ"},
	}
	for _, tc := range clean {
		t.Run(tc.name, func(t *testing.T) {
			if err := scanBundleForSecrets(bundleWithFile(tc.path, tc.content)); err != nil {
				t.Fatalf("scan refused ordinary source: %v", err)
			}
		})
	}
}

func asPluginError(err error, target **PluginError) bool {
	pluginErr, ok := err.(*PluginError)
	if ok {
		*target = pluginErr
	}
	return ok
}
