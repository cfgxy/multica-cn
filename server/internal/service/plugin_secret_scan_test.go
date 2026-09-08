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
		// A real binary asset carries no credential, and the patterns are
		// provider prefixes over fixed alphabets rather than entropy
		// heuristics — so compressed bytes match nothing and the file
		// publishes. It is scanned rather than skipped: see the NUL test below
		// for why "contains a NUL" cannot be an exemption.
		{"binary", "ui/font.woff", "\x00\x01wOFF\x00\x01\x00\x00" + strings.Repeat("\x7f\x00\xa3", 60)},
	}
	for _, tc := range clean {
		t.Run(tc.name, func(t *testing.T) {
			if err := scanBundleForSecrets(bundleWithFile(tc.path, tc.content)); err != nil {
				t.Fatalf("scan refused ordinary source: %v", err)
			}
		})
	}
}

// A NUL byte is something the author puts there. While one anywhere in a file
// skipped that file wholesale, appending a single zero byte to a script was a
// complete, one-character bypass of the whole scanner — and the author who
// wanted to ship a credential is exactly the author who would find it.
func TestANulByteDoesNotHideACredential(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"nul first", "\x00const id = \"AKIAQQQQQQQQQQQQQQQQ\";"},
		{"nul after", "const id = \"AKIAQQQQQQQQQQQQQQQQ\";\x00"},
		{"nul between", "const a = 1;\x00\nconst id = \"AKIAQQQQQQQQQQQQQQQQ\";"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := scanBundleForSecrets(bundleWithFile("ui/main.js", tc.content))
			if err == nil {
				t.Fatal("a NUL byte let a credential through the scanner")
			}
			if strings.Contains(err.Error(), "AKIAQQQQQQQQQQQQQQQQ") {
				t.Fatalf("the rejection quotes the secret: %q", err.Error())
			}
		})
	}
}

// The manifest is stored as the consented snapshot and returned by preview to
// every administrator who can see the listing. A key in a description or a
// default value is published exactly as surely as one in a script.
func TestTheManifestIsScannedToo(t *testing.T) {
	bundle := plugincontract.Bundle{
		Canonical: []byte(`{"manifest_version":1,"description":"use AKIAQQQQQQQQQQQQQQQQ to authenticate"}`),
	}
	err := scanBundleForSecrets(bundle)
	if err == nil {
		t.Fatal("a credential in the manifest was published: the manifest is served to every reader of the listing")
	}
	if !strings.Contains(err.Error(), "multica.plugin.json") {
		t.Fatalf("the rejection does not name the manifest: %q", err.Error())
	}
	if strings.Contains(err.Error(), "AKIAQQQQQQQQQQQQQQQQ") {
		t.Fatalf("the rejection quotes the secret: %q", err.Error())
	}
}

// A path is displayed and served verbatim, so a credential in a FILE NAME is
// published by the listing itself, before anyone opens the file.
//
// The rejection must not repeat it. The path is the one piece of text this
// scanner has to quote — it is the location the author needs — and quoting it
// unredacted turned "we refused to publish your key" into an API response, a
// server log and an issue comment that all contain the key.
func TestAFileNameIsScannedToo(t *testing.T) {
	const secret = "AKIAQQQQQQQQQQQQQQQQ"
	err := scanBundleForSecrets(bundleWithFile("ui/"+secret+".js", "export default 1;"))
	if err == nil {
		t.Fatal("a credential in a file name was published")
	}
	if !strings.Contains(err.Error(), "name of bundle file 1") {
		t.Fatalf("the rejection does not say the file NAME is at fault: %q", err.Error())
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the rejection quotes the credential it found in the file name: %q", err.Error())
	}
	// The location still has to be usable: a fully anonymous rejection sends
	// the author grepping a bundle they thought was clean.
	if !strings.Contains(err.Error(), "ui/[redacted].js") {
		t.Fatalf("the rejection redacted the path into uselessness: %q", err.Error())
	}
}

// The same path is also the subject line for the file's CONTENT, so redacting
// it in one message and not the other would leak the value anyway — this time
// from a file whose contents were the ordinary reason for rejection.
func TestAContentRejectionDoesNotQuoteASecretFileName(t *testing.T) {
	const nameSecret = "AKIAWWWWWWWWWWWWWWWW"
	const bodySecret = "ghp_" + "cccccccccccccccccccccccccccccccccccc"
	err := scanBundleForSecrets(plugincontract.Bundle{
		Files: []plugincontract.BundleFile{{
			// Clean name, so the loop reaches the content of the second file.
			Path: "ui/clean.js", Content: []byte("export default 1;"),
		}, {
			Path: "ui/" + nameSecret + ".js", Content: []byte("const t = \"" + bodySecret + "\";"),
		}},
	})
	if err == nil {
		t.Fatal("a credential in a file name was published")
	}
	if strings.Contains(err.Error(), nameSecret) {
		t.Fatalf("the rejection quotes the credential from the path: %q", err.Error())
	}
}

func asPluginError(err error, target **PluginError) bool {
	pluginErr, ok := err.(*PluginError)
	if ok {
		*target = pluginErr
	}
	return ok
}
