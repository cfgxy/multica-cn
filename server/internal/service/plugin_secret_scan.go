package service

import (
	"fmt"
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
// credential format, anywhere a reader of the published version could find it.
//
// The report gives the location and the 1-based line so the author can go
// straight to it, and the credential kind so they know what to rotate — and
// stops there. Echoing the match would copy the secret into a server log and an
// API response, which is the exact leak this function exists to prevent.
//
// Everything a published version carries is in scope, not just the files:
//
//   - The canonical manifest. It is stored as the consented snapshot and
//     returned by preview to any administrator who can see the listing, so a
//     key pasted into a description or a default value is published just as
//     surely as one in a script. "The shape is closed" bounds where a value can
//     sit, not whether it can be a credential.
//   - File PATHS as well as contents. A path is displayed and served verbatim,
//     and `deploy-AKIA....json` publishes the key in the listing itself.
func scanBundleForSecrets(bundle plugincontract.Bundle) error {
	if err := scanTextForSecrets("multica.plugin.json", string(bundle.Canonical)); err != nil {
		return err
	}
	for index, file := range bundle.Files {
		// The path names the location, so it travels into the rejection — and a
		// path can itself be the credential. It is therefore redacted before it
		// is ever put in a message, on both subjects: naming the file is the
		// point, quoting the key inside its name is the leak.
		//
		// The bundle position is added so a fully redacted name still points at
		// one entry rather than at "some file".
		safePath := redactSecrets(file.Path)
		location := fmt.Sprintf("the name of bundle file %d (%s)", index+1, safePath)
		// The path is scanned as its own subject: it is not part of the
		// content, and reporting a line number for it would be meaningless.
		if err := scanTextForSecrets(location, file.Path); err != nil {
			return err
		}
		if err := scanTextForSecrets(safePath, string(file.Content)); err != nil {
			return err
		}
	}
	return nil
}

// redactSecrets replaces every credential shape in a string with a placeholder.
//
// Used on text the rejection has to QUOTE — a file path is the location an
// author needs, and it is also somewhere a key can sit. Replacing rather than
// dropping keeps the surrounding path readable, so `ui/[redacted].js` still
// says which directory and which extension.
func redactSecrets(text string) string {
	for _, pattern := range pluginSecretPatterns {
		text = pattern.Re.ReplaceAllString(text, "[redacted]")
	}
	return text
}

// scanTextForSecrets reports the first credential shape in one subject.
//
// A NUL byte does not stop the scan. Binary assets were skipped wholesale to
// keep compressed bytes from producing false positives, but "contains a NUL"
// is something an author controls: appending one zero byte to a script turned
// the whole file invisible to the scanner, which made the check optional for
// anyone who knew. So the text is split ON the NUL bytes and every stretch
// between them is scanned. A genuine binary still matches nothing — the
// patterns are provider prefixes with fixed alphabets, not entropy heuristics —
// while a script with a NUL smuggled into it no longer buys immunity.
func scanTextForSecrets(subject, text string) error {
	if text == "" {
		return nil
	}
	// Offsets are tracked across the split so the reported line is the line in
	// the original text, not in the fragment.
	offset := 0
	for _, segment := range strings.Split(text, "\x00") {
		for _, pattern := range pluginSecretPatterns {
			loc := pattern.Re.FindStringIndex(segment)
			if loc == nil {
				continue
			}
			line := 1 + strings.Count(text[:offset+loc[0]], "\n")
			return pluginErrf(PluginErrorInvalid,
				"%s looks like it contains a %s (line %d). A published version is immutable and is served to every workspace that installs it, so remove the credential and rotate it before publishing. Values a plugin needs at runtime belong in a `secret` configuration field, which the server never returns.",
				subject, pattern.Name, line)
		}
		offset += len(segment) + 1
	}
	return nil
}
