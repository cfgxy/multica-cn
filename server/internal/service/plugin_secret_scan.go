package service

import (
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// Publishing scans for credentials.
//
// A plugin artifact is served verbatim to every workspace that installs it, and
// the directory makes that "every workspace on the instance". An author who
// leaves a token in a bundled file has therefore not made a private mistake —
// they have published the token. There is no unpublish that helps either: a
// version is immutable, so the only remedy after the fact is rotation.
//
// So the scan runs at publish and refuses. It is a coarse net by design: the
// patterns below match credential FORMATS that are unambiguous on sight, and
// nothing that merely looks secret-ish. A false positive costs an author one
// confusing rejection; a false negative costs somebody their production key.

// secretPattern is one credential shape the scanner refuses.
type secretPattern struct {
	// Name is what the rejection tells the author. It names the KIND of
	// credential, never the value — the error text travels into logs and issue
	// comments, so it must stay safe to paste.
	Name string
	Re   *regexp.Regexp
}

// pluginSecretPatterns are formats issued by a provider with a fixed prefix and
// length, plus the PEM private key header. Every one of them is a token shape no
// human types by accident, which is what keeps this list free of the generic
// `password = "..."` heuristics that make scanners unusable.
var pluginSecretPatterns = []secretPattern{
	{"AWS access key ID", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"GitHub token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"Slack token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"Anthropic API key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)},
	{"OpenAI API key", regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9]{32,}`)},
	{"Google API key", regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{"Stripe secret key", regexp.MustCompile(`(?:sk|rk)_live_[0-9A-Za-z]{16,}`)},
	{"private key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
	{"JSON Web Token", regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
}

// scanBundleForSecrets refuses a bundle carrying anything that matches a
// credential format.
//
// The report gives the file and the 1-based line so the author can go straight
// to it, and the credential kind so they know what to rotate — and stops there.
// Echoing the match would copy the secret into a server log and an API response,
// which is the exact leak this function exists to prevent.
//
// Only the FILES are scanned. The manifest itself is not: it is re-encoded and
// stored as the consented snapshot, its shape is closed, and it has no free-text
// field wide enough to hide a key in — a secret an author put in configuration
// belongs in a `secret` config field, which is write-only and never leaves the
// server at all.
func scanBundleForSecrets(bundle plugincontract.Bundle) error {
	for _, file := range bundle.Files {
		content := string(file.Content)
		// A binary asset (an icon, a font) is not source an author edits, and
		// scanning it produces only false positives against compressed bytes.
		if strings.IndexByte(content, 0) >= 0 {
			continue
		}
		for _, pattern := range pluginSecretPatterns {
			loc := pattern.Re.FindStringIndex(content)
			if loc == nil {
				continue
			}
			line := 1 + strings.Count(content[:loc[0]], "\n")
			return pluginErrf(PluginErrorInvalid,
				"%s looks like it contains a %s (line %d). A published version is immutable and is served to every workspace that installs it, so remove the credential and rotate it before publishing. Values a plugin needs at runtime belong in a `secret` configuration field, which the server never returns.",
				file.Path, pattern.Name, line)
		}
	}
	return nil
}
