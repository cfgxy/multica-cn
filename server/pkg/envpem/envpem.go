// Package envpem normalizes PEM private keys that arrive via environment
// variables, so every reader accepts the shapes operators actually write.
//
// The repository Makefile includes .env directly (`include $(ENV_FILE)`),
// and GNU make rejects a multi-line variable value — every make target dies
// with "missing separator" the moment .env carries a real multi-line PEM.
// The only make-compatible form is single-line with literal `\n` escapes,
// usually still wrapped in the double quotes the dotenv guidance asked for.
// Docker Compose passes that string through verbatim: quotes included,
// escapes unexpanded. jwt.ParseRSAPrivateKeyFromPEM then fails on both
// counts and the GitHub App silently degrades (installations show as
// "unknown", PR snapshots and repo browsing go dark).
package envpem

import "strings"

// NormalizePrivateKey returns a PEM private key string in the form
// jwt.ParseRSAPrivateKeyFromPEM accepts. It is tolerant, never lossy for
// valid input:
//
//   - surrounding whitespace is trimmed;
//   - one pair of wrapping quotes ("…" or '…') is stripped — dotenv
//     implementations disagree on whether they consume the quotes, so both
//     shapes reach the process;
//   - a value with no real newlines has its literal \n escapes expanded.
//     A genuine multi-line PEM never contains a backslash, so the expansion
//     cannot corrupt a key that was already valid.
func NormalizePrivateKey(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	if !strings.Contains(s, "\n") {
		s = strings.ReplaceAll(s, `\n`, "\n")
	}
	return s
}
