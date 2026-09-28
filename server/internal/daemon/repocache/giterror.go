package repocache

import (
	"regexp"
	"strings"
)

// GitFailureKind names why a git network operation failed. The daemon clones
// repositories with the operator's own credentials (an ssh key or a credential
// helper), so when a private repo fails the operator needs to know WHICH of
// the four causes it was — "git clone failed" alone sends them looking in the
// wrong place. The value is embedded in the returned error, which is what ends
// up in the task log.
type GitFailureKind string

const (
	GitFailureUnknown      GitFailureKind = "unknown"
	GitFailureNetwork      GitFailureKind = "network_unreachable"
	GitFailureTLS          GitFailureKind = "tls_certificate"
	GitFailurePermission   GitFailureKind = "permission_denied"
	GitFailureRepoNotFound GitFailureKind = "repository_not_found"
)

// Ordered because git output overlaps: a certificate failure also says
// "unable to access", and GitLab's "could not be found" message mentions
// permission. First match wins, so the more specific cause is listed first.
var gitFailurePatterns = []struct {
	kind     GitFailureKind
	patterns []string
}{
	{GitFailureTLS, []string{
		"server certificate verification failed",
		"ssl certificate problem",
		"certificate verify failed",
		"unable to get local issuer certificate",
		"certificate has expired",
		"self signed certificate",
		"x509:",
	}},
	// Before network: an ssh key rejection also prints "Could not read from
	// remote repository", and before not-found because GitHub answers an
	// unauthorized private repo with "Repository not found" plus an auth
	// failure — the credential is the actionable cause in that pair.
	{GitFailurePermission, []string{
		"permission denied",
		"authentication failed",
		"access denied",
		"could not read username",
		"could not read password",
		"403 forbidden",
		"invalid username or password",
		"terminal prompts disabled",
	}},
	{GitFailureNetwork, []string{
		"could not resolve host",
		"failed to connect to",
		"connection refused",
		"connection timed out",
		"connection reset by peer",
		"network is unreachable",
		"no route to host",
		"operation timed out",
		"timed out",
	}},
	{GitFailureRepoNotFound, []string{
		"repository not found",
		"does not appear to be a git repository",
		"could not be found",
		"not found",
		"no such file or directory",
	}},
}

// classifyGitFailure maps git's combined output to one of the four causes.
// Unrecognised output stays GitFailureUnknown rather than being forced into
// the nearest bucket — a wrong class is worse than no class.
//
// Matching English text is only safe because gitEnv() pins LC_ALL=C; on a
// localized host git would emit translated messages and every pattern here
// would miss.
func classifyGitFailure(output string) GitFailureKind {
	lower := strings.ToLower(output)
	if strings.TrimSpace(lower) == "" {
		return GitFailureUnknown
	}
	for _, group := range gitFailurePatterns {
		for _, p := range group.patterns {
			if strings.Contains(lower, p) {
				return group.kind
			}
		}
	}
	return GitFailureUnknown
}

// httpCredentialPattern matches the userinfo of an http(s) URL. Only http(s)
// carries secrets there: ssh has no password field, so an ssh userinfo is a
// username and keeping it makes the error readable.
var httpCredentialPattern = regexp.MustCompile(`(?i)(https?)://[^/@\s'"]+@`)

// redactGitCredentials strips credentials embedded in http(s) URLs. Current
// git already omits userinfo from its own "unable to access" line, so this is
// the second layer: it covers what git does NOT sanitize — a server-side
// `remote:` line echoing the request URL, a URL a credential helper or an
// insteadOf rewrite put on the command line, and older git builds.
func redactGitCredentials(s string) string {
	return httpCredentialPattern.ReplaceAllString(s, "$1://***@")
}

// gitNetworkFailure wraps a failed git network command with its cause class
// and credential-free output.
func gitNetworkFailure(op string, out []byte, err error) error {
	text := strings.TrimSpace(string(out))
	return &GitNetworkError{
		Op:   op,
		Kind: classifyGitFailure(text),
		// Redact git output before it becomes a logged error value.
		Output: redactGitCredentials(text),
		Err:    err,
	}
}

// GitNetworkError is the error returned by clone/fetch failures. It keeps the
// cause class machine-readable so callers can branch on it instead of
// substring-matching git output again.
type GitNetworkError struct {
	Op     string
	Kind   GitFailureKind
	Output string
	Err    error
}

func (e *GitNetworkError) Error() string {
	// Keep the original cause for errors.Is/As, but never format a URL it may contain.
	if e.Output == "" {
		return e.Op + " [" + string(e.Kind) + "]: " + redactGitCredentials(e.Err.Error())
	}
	return e.Op + " [" + string(e.Kind) + "]: " + e.Output + ": " + redactGitCredentials(e.Err.Error())
}

func (e *GitNetworkError) Unwrap() error { return e.Err }
