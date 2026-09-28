package repocache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
)

// The four failure classes RUYI-249 requires a daemon operator to be able to
// tell apart when a private-repo clone fails. Each sample is real git output
// (GitHub, GitLab and plain openssh wording), because the whole point of the
// classifier is that it survives the exact strings git emits.
func TestClassifyGitFailure(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   GitFailureKind
	}{
		{
			"dns failure",
			"fatal: unable to access 'https://gitlab.example.com/g/s/r.git/': Could not resolve host: gitlab.example.com",
			GitFailureNetwork,
		},
		{
			"connection refused",
			"fatal: unable to access 'https://gitlab.example.com/g/r.git/': Failed to connect to gitlab.example.com port 443: Connection refused",
			GitFailureNetwork,
		},
		{
			"ssh no route",
			"ssh: connect to host gitlab.example.com port 22: No route to host\r\nfatal: Could not read from remote repository.",
			GitFailureNetwork,
		},
		{
			"self-signed certificate",
			"fatal: unable to access 'https://gitlab.example.com/g/r.git/': server certificate verification failed. CAfile: none CRLfile: none",
			GitFailureTLS,
		},
		{
			"openssl certificate problem",
			"fatal: unable to access 'https://gitlab.example.com/g/r.git/': SSL certificate problem: unable to get local issuer certificate",
			GitFailureTLS,
		},
		{
			"ssh key rejected",
			"git@gitlab.example.com: Permission denied (publickey).\r\nfatal: Could not read from remote repository.",
			GitFailurePermission,
		},
		{
			"http basic auth rejected",
			"remote: HTTP Basic: Access denied\nfatal: Authentication failed for 'https://gitlab.example.com/g/r.git/'",
			GitFailurePermission,
		},
		{
			"no credential helper configured",
			"fatal: could not read Username for 'https://gitlab.example.com': terminal prompts disabled",
			GitFailurePermission,
		},
		{
			"gitlab project missing",
			"remote: The project you were looking for could not be found or you don't have permission to view it.\nfatal: repository 'https://gitlab.example.com/g/r.git/' not found",
			GitFailureRepoNotFound,
		},
		{
			"plain 404",
			"fatal: repository 'https://gitlab.example.com/g/r.git/' not found",
			GitFailureRepoNotFound,
		},
		{
			"ssh path missing",
			"fatal: 'g/s/r.git' does not appear to be a git repository",
			GitFailureRepoNotFound,
		},
		{
			"unrecognised",
			"fatal: the remote end hung up unexpectedly",
			GitFailureUnknown,
		},
		{"empty", "", GitFailureUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyGitFailure(tc.output); got != tc.want {
				t.Errorf("classifyGitFailure() = %q, want %q\noutput: %s", got, tc.want, tc.output)
			}
		})
	}
}

// GitHub answers a private repo the caller cannot see with "Repository not
// found" AND an auth failure. Classifying that as permission_denied is a
// deliberate choice: the actionable cause is the credential, and a missing
// repo reads the same way from outside.
func TestClassifyGitFailurePrefersPermissionWhenBothSignalsPresent(t *testing.T) {
	output := "remote: Repository not found.\nfatal: Authentication failed for 'https://github.com/org/private.git/'"
	if got := classifyGitFailure(output); got != GitFailurePermission {
		t.Errorf("classifyGitFailure() = %q, want %q", got, GitFailurePermission)
	}
}

// A certificate error also carries "unable to access", which the network
// patterns would otherwise claim. Ordering, not pattern overlap, is what keeps
// the two apart — so pin it.
func TestClassifyGitFailureTLSBeatsNetwork(t *testing.T) {
	output := "fatal: unable to access 'https://gitlab.example.com/g/r.git/': SSL certificate problem: self signed certificate"
	if got := classifyGitFailure(output); got != GitFailureTLS {
		t.Errorf("classifyGitFailure() = %q, want %q", got, GitFailureTLS)
	}
}

func TestRedactGitCredentials(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   string
		forbid string
	}{
		{
			"token in https userinfo",
			"fatal: unable to access 'https://oauth2:glpat-SECRETVALUE@gitlab.example.com/g/r.git/': 403",
			"fatal: unable to access 'https://***@gitlab.example.com/g/r.git/': 403",
			"glpat-SECRETVALUE",
		},
		{
			"bare pat without password",
			"remote: rejected https://ghp_SECRETVALUE@github.com/org/repo.git",
			"remote: rejected https://***@github.com/org/repo.git",
			"ghp_SECRETVALUE",
		},
		{
			"http scheme too",
			"http://user:pw@internal.git/x.git",
			"http://***@internal.git/x.git",
			"pw@",
		},
		{
			// ssh userinfo is a username, never a secret — keeping it makes
			// the error readable ("which account did we present?").
			"ssh user preserved",
			"ssh://git@gitlab.example.com/g/r.git",
			"ssh://git@gitlab.example.com/g/r.git",
			"",
		},
		{
			"scp shorthand untouched",
			"git@gitlab.example.com:g/s/r.git: Permission denied (publickey).",
			"git@gitlab.example.com:g/s/r.git: Permission denied (publickey).",
			"",
		},
		{
			"multiple occurrences",
			"a https://u:p1@h/x.git b https://u:p2@h/y.git",
			"a https://***@h/x.git b https://***@h/y.git",
			"p2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactGitCredentials(tc.in)
			if got != tc.want {
				t.Errorf("redactGitCredentials() = %q, want %q", got, tc.want)
			}
			if tc.forbid != "" && strings.Contains(got, tc.forbid) {
				t.Errorf("redactGitCredentials() leaked %q in %q", tc.forbid, got)
			}
		})
	}
}

func TestGitNetworkFailureRedactsCredentialFromError(t *testing.T) {
	const outputSecret = "example-output-password"
	const causeSecret = "example-cause-password"
	err := gitNetworkFailure("git clone --bare", []byte("fatal: unable to access 'https://user:"+outputSecret+"@gitlab.test/g/r.git': Authentication failed"), errors.New("https://user:"+causeSecret+"@gitlab.test/g/r.git: exit status 128"))
	if strings.Contains(err.Error(), outputSecret) || strings.Contains(err.Error(), causeSecret) {
		t.Fatal("git network error included URL credentials")
	}
	if !strings.Contains(err.Error(), "https://***@gitlab.test/g/r.git") {
		t.Fatal("git network error lost the sanitized remote host")
	}
}

func TestCacheSyncLogsRedactRepoURL(t *testing.T) {
	const secret = "example-test-password"
	url := "http://user:" + secret + "@127.0.0.1:1/group/repo.git"
	for _, cached := range []bool{false, true} {
		name := "clone"
		if cached {
			name = "fetch"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			var log bytes.Buffer
			cache := New(root, slog.New(slog.NewTextHandler(&log, nil)))
			if cached {
				barePath := cache.BarePath("workspace", url)
				if out, err := exec.Command("git", "init", "--bare", barePath).CombinedOutput(); err != nil {
					t.Fatalf("initialize bare fixture: %v: %s", err, out)
				}
				if out, err := exec.Command("git", "-C", barePath, "remote", "add", "origin", url).CombinedOutput(); err != nil {
					t.Fatalf("configure fixture remote: %v: %s", err, out)
				}
			}
			if err := cache.SyncContext(context.Background(), "workspace", []RepoInfo{{URL: url}}); err == nil {
				t.Fatal("expected closed loopback port to reject git operation")
			}
			output := log.String()
			if strings.Count(output, "http://***@127.0.0.1:1/group/repo.git") != 2 {
				t.Fatal("cache start and failure events must both include a sanitized URL")
			}
			if strings.Contains(output, secret) {
				t.Fatal("cache log included URL credentials")
			}
			if !strings.Contains(output, "repo cache: "+name+" failed") {
				t.Fatal("cache log omitted failure event")
			}
		})
	}
}

// End-to-end over the real git binary: a clone against a closed local port
// must come back classified. Loopback only — no external network.
//
// Deliberately NOT asserting credential absence here: current git already
// strips userinfo from its own "unable to access" line, so such an assertion
// would pass with redactGitCredentials removed — an assertion that cannot
// fail is not evidence. Redaction is pinned in TestRedactGitCredentials,
// where the input is the unredacted string.
func TestGitCloneBareClassifiesFailure(t *testing.T) {
	dest := t.TempDir() + "/bare.git"
	err := gitCloneBare("http://oauth2:glpat-LEAKCANARY@127.0.0.1:1/g/s/r.git", dest)
	if err == nil {
		t.Fatal("expected the clone to fail against a closed port")
	}
	var netErr *GitNetworkError
	if !errors.As(err, &netErr) {
		t.Fatalf("error is not a *GitNetworkError: %T %v", err, err)
	}
	if netErr.Kind != GitFailureNetwork {
		t.Errorf("Kind = %q, want %q (output: %s)", netErr.Kind, GitFailureNetwork, netErr.Output)
	}
	if !strings.Contains(err.Error(), string(GitFailureNetwork)) {
		t.Errorf("the class must reach the error text an operator reads: %s", err.Error())
	}
}
