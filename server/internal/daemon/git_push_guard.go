package daemon

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Source: cfgxy/ruyi tools/git-push-guard at merged RUYI-403 commit
// 2fabc9c9910ceb032a2fa42b9c1a09a599a50594. Keep this as the sole git-push
// policy implementation; task-specific policy is supplied through its config.
//
//go:embed guards/git-push-guard
var gitPushGuardSource []byte

// Source: cfgxy/ruyi tools/gh-pr-guard from the RUYI-401 runtime installation,
// verified against active SHA256 1a9392e53d673b8f2f2391c715872794ef69f8e5d0996fdb5f05912bae05855c.
//
//go:embed guards/gh-pr-guard
var ghPRGuardSource []byte

var githubRepoPath = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func prepareTaskGitPushGuard(cacheRoot, taskID string, repos []RepoData) (string, string, error) {
	if strings.TrimSpace(taskID) == "" {
		return "", "", fmt.Errorf("git push guard: task id missing; refusing to launch without task-scoped policy")
	}
	var canonical []string
	seen := make(map[string]string, len(repos))
	for _, repo := range repos {
		name, err := canonicalGitHubRepo(repo.URL)
		if err != nil {
			return "", "", fmt.Errorf("git push guard: project repository %q is untrusted; confirm its GitHub URL in project resources before retrying: %w", repo.URL, err)
		}
		upstream := ""
		if strings.TrimSpace(repo.Upstream) != "" {
			var upstreamErr error
			if githubRepoPath.MatchString(strings.TrimSpace(repo.Upstream)) {
				upstream = strings.ToLower(strings.TrimSpace(repo.Upstream))
			} else {
				upstream, upstreamErr = canonicalGitHubRepo(repo.Upstream)
			}
			if upstreamErr != nil {
				return "", "", fmt.Errorf("git push guard: invalid confirmed upstream %q for %s; update the project github_repo resource: %w", repo.Upstream, name, upstreamErr)
			}
			if upstream == name {
				return "", "", fmt.Errorf("git push guard: confirmed upstream %q must differ from canonical %s", repo.Upstream, name)
			}
		}
		if previous, ok := seen[name]; ok {
			if previous != upstream {
				return "", "", fmt.Errorf("git push guard: conflicting upstream confirmation for %s; keep one consistent github_repo resource", name)
			}
			continue
		}
		seen[name] = upstream
		line := name
		if upstream != "" {
			line += " upstream=" + upstream
		}
		canonical = append(canonical, line)
	}
	config := []byte(strings.Join(canonical, "\n"))
	if len(config) > 0 {
		config = append(config, '\n')
	}
	digest := sha256.Sum256([]byte(taskID))
	dir := filepath.Join(cacheRoot, "multica", "git-push-guard", hex.EncodeToString(digest[:]))
	guardPath := filepath.Join(dir, "git-push-guard")
	ghGuardPath := filepath.Join(dir, "gh-pr-guard")
	configPath := filepath.Join(dir, "canonical-repos.conf")
	if err := writeImmutableGuardFile(guardPath, gitPushGuardSource, 0o700); err != nil {
		return "", "", fmt.Errorf("git push guard: materialize versioned guard: %w", err)
	}
	if err := writeImmutableGuardFile(ghGuardPath, ghPRGuardSource, 0o700); err != nil {
		return "", "", fmt.Errorf("gh guard: materialize versioned RUYI-401 guard: %w", err)
	}
	if err := writeImmutableGuardFile(configPath, config, 0o600); err != nil {
		return "", "", fmt.Errorf("git push guard: policy drift detected at %s; confirm project repository resources and restart the task: %w", configPath, err)
	}
	return guardPath, configPath, nil
}

func canonicalGitHubRepo(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	var host, path string
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && (u.Scheme != "ssh" || u.Port() != "22")) {
			return "", fmt.Errorf("expected a public GitHub repository URL")
		}
		switch u.Scheme {
		case "https", "http":
			if u.User != nil {
				return "", fmt.Errorf("expected a public GitHub repository URL")
			}
		case "ssh":
			if u.User == nil || u.User.Username() != "git" {
				return "", fmt.Errorf("expected a GitHub SSH repository URL for user git")
			}
		default:
			return "", fmt.Errorf("expected a public GitHub repository URL")
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.EscapedPath(), "/")
	} else if strings.HasPrefix(value, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(value, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("expected a GitHub SSH repository URL")
		}
		host, path = parts[0], parts[1]
	} else {
		return "", fmt.Errorf("expected a GitHub repository URL")
	}
	if !strings.EqualFold(host, "github.com") {
		return "", fmt.Errorf("host %q is not github.com", host)
	}
	path = strings.TrimSuffix(path, ".git")
	if !githubRepoPath.MatchString(path) {
		return "", fmt.Errorf("URL must identify exactly owner/repo")
	}
	return strings.ToLower(path), nil
}

func writeImmutableGuardFile(path string, content []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if !bytes.Equal(existing, content) || info.Mode().Perm() != mode {
			return fmt.Errorf("existing file differs from the versioned policy")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".guard-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, content) {
			return nil
		}
		return err
	}
	return nil
}
