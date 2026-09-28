package util

import "strings"

// NormalizePEMKey turns a PEM private key read from an environment variable
// into the real multi-line text crypto/x509 expects, accepting both writings an
// operator can put in a `.env` file:
//
//   - a double-quoted value carrying real line breaks, which is what every
//     deployment configured before RUYI-216 has on disk, and
//   - a single physical line whose breaks are written as the two-character
//     escape `\n` (or `\r\n`).
//
// The single-line writing is the recommended one because `Makefile` reads the
// same file through `include`, and GNU make has no notion of a value spanning
// lines: one multi-line value fails the whole file with `missing separator`,
// taking every make target down with it.
//
// Surrounding quotes are stripped because that single line still has to be
// quoted for the shell: the dev scripts read the same file with `set -a; .
// env`, where an unquoted PEM header splits on its spaces. Every dotenv-style
// loader (compose `env_file`, the shell) strips those quotes itself, but make's
// `include` does not and this repository's Makefile exports what it included —
// so a quoted value can reach the process with the quotes still attached.
//
// Decoding the escapes unconditionally is safe: a PEM block is base64 plus
// `+`, `/`, `=`, `-` and line breaks, so neither a backslash nor a quote can
// have come from the key material itself.
//
// The value is never logged or echoed by this helper, and a value it cannot
// make sense of is returned as-is for the caller's parser to reject — the
// parse failure stays the single place that reports a bad key, without the key.
func NormalizePEMKey(raw string) string {
	value := strings.TrimSpace(raw)
	for _, quote := range []string{`"`, `'`} {
		if len(value) >= 2 && strings.HasPrefix(value, quote) && strings.HasSuffix(value, quote) {
			value = strings.TrimSpace(value[1 : len(value)-1])
			break
		}
	}
	if value == "" {
		return ""
	}
	if strings.Contains(value, `\`) {
		value = strings.ReplaceAll(value, `\r\n`, "\n")
		value = strings.ReplaceAll(value, `\n`, "\n")
	}
	return strings.TrimSpace(value)
}
