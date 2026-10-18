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

var githubRepoPath = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func prepareTaskGitPushGuard(cacheRoot, taskID string, repos []RepoData) (string, string, error) {
	if strings.TrimSpace(taskID) == "" {
		return "", "", fmt.Errorf("git push guard: task id missing; refusing to launch without task-scoped policy")
	}
	var canonical []string
	seen := make(map[string]struct{}, len(repos))
	for _, repo := range repos {
		name, err := canonicalGitHubRepo(repo.URL)
		if err != nil {
			return "", "", fmt.Errorf("git push guard: project repository %q is untrusted; confirm its GitHub URL in project resources before retrying: %w", repo.URL, err)
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			canonical = append(canonical, name)
		}
	}
	config := []byte(strings.Join(canonical, "\n"))
	if len(config) > 0 {
		config = append(config, '\n')
	}
	digest := sha256.Sum256([]byte(taskID))
	dir := filepath.Join(cacheRoot, "multica", "git-push-guard", hex.EncodeToString(digest[:]))
	guardPath := filepath.Join(dir, "git-push-guard")
	configPath := filepath.Join(dir, "canonical-repos.conf")
	if err := writeImmutableGuardFile(guardPath, gitPushGuardSource, 0o700); err != nil {
		return "", "", fmt.Errorf("git push guard: materialize versioned guard: %w", err)
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
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
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
