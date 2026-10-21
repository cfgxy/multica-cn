package fileaccess

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Kind string

const (
	KindList     Kind = "file_list"
	KindMetadata Kind = "file_metadata"
	KindRead     Kind = "file_read_chunk"

	MaxListEntries = 200
	MaxChunkBytes  = 64 * 1024
	MaxFileBytes   = 32 * 1024 * 1024
)

var (
	ErrorHostNotRegistered     = errors.New("host_not_registered")
	ErrorTargetDisabled        = errors.New("target_disabled")
	ErrorTargetUnreachable     = errors.New("target_unreachable")
	ErrorPermissionDenied      = errors.New("permission_denied")
	ErrorPathNotAllowed        = errors.New("path_not_allowed")
	ErrorSensitivePathDenied   = errors.New("sensitive_path_denied")
	ErrorSymlinkEscapeDenied   = errors.New("symlink_escape_denied")
	ErrorNotFound              = errors.New("not_found")
	ErrorNotRegularFile        = errors.New("not_regular_file")
	ErrorRangeInvalid          = errors.New("range_invalid")
	ErrorFileChanged           = errors.New("file_changed")
	ErrorFileTooLarge          = errors.New("file_too_large")
	ErrorUnsupportedResolution = errors.New("unsupported_path_resolution")
)

type Root struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type DenyRule struct {
	ID          string `json:"id"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin"`
}

type DenyPolicy struct {
	Version int64      `json:"version"`
	Rules   []DenyRule `json:"rules"`
}

func DefaultDenyPolicy() DenyPolicy {
	patterns := []struct{ id, pattern, description string }{
		{"ssh", "**/.ssh/**", "SSH keys and configuration"},
		{"gnupg", "**/.gnupg/**", "GPG keys and configuration"},
		{"aws-credentials", "**/.aws/credentials", "AWS credentials"},
		{"azure-credentials", "**/.azure/**", "Azure credentials"},
		{"gcloud-credentials", "**/.config/gcloud/**", "Google Cloud credentials"},
		{"kube-config", "**/.kube/config", "Kubernetes credentials"},
		{"docker-config", "**/.docker/config.json", "Docker registry credentials"},
		{"env-files", "**/.env*", "Environment configuration files"},
		{"netrc", "**/.netrc", "Network login credentials"},
		{"git-credentials", "**/.git-credentials", "Git credentials"},
		{"credential-files", "**/*credential*", "Credential files"},
		{"secret-files", "**/*secret*", "Secret files"},
		{"token-files", "**/*token*", "Token files"},
		{"npmrc", "**/.npmrc", "Package registry credentials"},
		{"pypirc", "**/.pypirc", "Python package registry credentials"},
		{"private-key-pem", "**/*.pem", "Private keys and certificate bundles"},
		{"private-key-key", "**/*.key", "Private keys"},
		{"ssh-private-key-names", "**/id_*", "SSH private keys"},
		{"ai-client-auth", "**/.config/*/auth.json", "AI client authentication"},
	}
	rules := make([]DenyRule, 0, len(patterns))
	for _, item := range patterns {
		rules = append(rules, DenyRule{ID: item.id, Pattern: item.pattern, Description: item.description, Enabled: true, Builtin: true})
	}
	return DenyPolicy{Version: 1, Rules: rules}
}

// Target is an explicit registration. An empty ACL is deny-all.
type Target struct {
	ID           string   `json:"id"`
	RuntimeID    string   `json:"runtime_id,omitempty"`
	Enabled      bool     `json:"enabled"`
	WorkspaceIDs []string `json:"workspace_ids"`
	MemberIDs    []string `json:"member_ids,omitempty"`
	Roots        []Root   `json:"roots"`
}

type Request struct {
	TargetID        string     `json:"host"`
	Kind            Kind       `json:"kind"`
	Path            string     `json:"path"`
	Offset          int64      `json:"offset,omitempty"`
	Length          int64      `json:"length,omitempty"`
	Limit           int        `json:"limit,omitempty"`
	Cursor          string     `json:"cursor,omitempty"`
	ExpectedSize    *int64     `json:"expected_size,omitempty"`
	ExpectedModTime *time.Time `json:"expected_mtime,omitempty"`
}

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

type Metadata struct {
	TargetID    string    `json:"target_id"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mtime"`
	ContentType string    `json:"content_type"`
}

type ListResult struct {
	TargetID string  `json:"target_id"`
	Path     string  `json:"path"`
	Entries  []Entry `json:"entries"`
	Next     string  `json:"next_cursor,omitempty"`
}

type ReadResult struct {
	TargetID string `json:"target_id"`
	Path     string `json:"path"`
	Offset   int64  `json:"offset"`
	Length   int    `json:"length"`
	EOF      bool   `json:"eof"`
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}

func NormalizePath(path string) (string, error) {
	return NormalizePathWithPolicy(path, DefaultDenyPolicy())
}

func NormalizePathWithPolicy(path string, policy DenyPolicy) (string, error) {
	clean, err := NormalizePathUnfiltered(path)
	if err != nil {
		return "", err
	}
	if policy.Denies(clean) {
		return "", ErrorSensitivePathDenied
	}
	return clean, nil
}

func NormalizePathUnfiltered(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return "", ErrorPathNotAllowed
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", ErrorPathNotAllowed
		}
	}
	clean := filepath.Clean(path)
	return clean, nil
}

func (p DenyPolicy) Denies(candidate string) bool {
	for _, rule := range p.Rules {
		if rule.Enabled && MatchDenyPattern(rule.Pattern, candidate) {
			return true
		}
	}
	return false
}

func MatchDenyPattern(pattern, candidate string) bool {
	pattern = strings.ToLower(filepath.ToSlash(pattern))
	candidate = strings.ToLower(filepath.ToSlash(candidate))
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	candidateParts := strings.Split(strings.TrimPrefix(candidate, "/"), "/")
	return matchPathParts(patternParts, candidateParts)
}

func matchPathParts(pattern, candidate []string) bool {
	matchedPrefix := make([]bool, len(candidate)+1)
	matchedPrefix[0] = true
	for _, partPattern := range pattern {
		next := make([]bool, len(candidate)+1)
		if partPattern == "**" {
			next[0] = matchedPrefix[0]
			for i := 1; i <= len(candidate); i++ {
				next[i] = matchedPrefix[i] || next[i-1]
			}
		} else {
			for i := 1; i <= len(candidate); i++ {
				matched, err := path.Match(partPattern, candidate[i-1])
				next[i] = matchedPrefix[i-1] && err == nil && matched
			}
		}
		matchedPrefix = next
	}
	return matchedPrefix[len(candidate)]
}

// ResolveRootPath validates lexical and resolved containment before a caller
// opens a path with OpenBeneath. OpenBeneath repeats containment at the kernel
// boundary to close the check/open race.
func ResolveRootPath(roots []string, requested string) (string, error) {
	return ResolveRootPathWithPolicy(roots, requested, DefaultDenyPolicy())
}

func ResolveRootPathWithPolicy(roots []string, requested string, policy DenyPolicy) (string, error) {
	path, err := NormalizePathWithPolicy(requested, policy)
	if err != nil {
		return "", err
	}
	for _, configured := range roots {
		root, err := filepath.Abs(configured)
		if err != nil {
			continue
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", ErrorNotFound
			}
			continue
		}
		resolvedRel, err := filepath.Rel(root, resolved)
		if err != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(os.PathSeparator)) || filepath.IsAbs(resolvedRel) {
			return "", ErrorSymlinkEscapeDenied
		}
		if policy.Denies(resolved) {
			return "", ErrorSensitivePathDenied
		}
		return resolved, nil
	}
	return "", ErrorPathNotAllowed
}

func ValidateRequest(req Request) error {
	return ValidateRequestWithPolicy(req, DefaultDenyPolicy())
}

func ValidateRequestStructure(req Request) error {
	return validateRequest(req, nil)
}

func ValidateRequestWithPolicy(req Request, policy DenyPolicy) error {
	return validateRequest(req, &policy)
}

func validateRequest(req Request, policy *DenyPolicy) error {
	if req.TargetID == "" {
		return ErrorHostNotRegistered
	}
	var err error
	if policy == nil {
		_, err = NormalizePathUnfiltered(req.Path)
	} else {
		_, err = NormalizePathWithPolicy(req.Path, *policy)
	}
	if err != nil {
		return err
	}
	switch req.Kind {
	case KindList:
		if req.Limit < 0 || req.Limit > MaxListEntries {
			return ErrorRangeInvalid
		}
	case KindMetadata:
	case KindRead:
		if req.Offset < 0 || req.Length < 1 || req.Length > MaxChunkBytes || req.Offset > MaxFileBytes {
			return ErrorRangeInvalid
		}
	default:
		return fmt.Errorf("%w: unsupported operation", ErrorPermissionDenied)
	}
	return nil
}

func ValidateDenyPolicy(policy DenyPolicy) error {
	if policy.Version < 1 || len(policy.Rules) > 256 {
		return ErrorRangeInvalid
	}
	seen := make(map[string]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.ID == "" || len(rule.ID) > 64 || len(rule.Pattern) > 512 || len(rule.Description) > 256 ||
			strings.ContainsAny(rule.ID, "/\\\x00") || !filepath.IsAbs(rule.Pattern) && !strings.HasPrefix(rule.Pattern, "**/") {
			return ErrorRangeInvalid
		}
		if _, exists := seen[rule.ID]; exists {
			return ErrorRangeInvalid
		}
		seen[rule.ID] = struct{}{}
		for _, part := range strings.Split(filepath.ToSlash(rule.Pattern), "/") {
			if part == ".." || strings.ContainsRune(part, '\x00') {
				return ErrorRangeInvalid
			}
			if part != "**" {
				if _, err := path.Match(part, "sample"); err != nil {
					return ErrorRangeInvalid
				}
			}
		}
	}
	return nil
}

func HasTargetAccess(target Target, workspaceID, memberID string) error {
	if target.ID == "" {
		return ErrorHostNotRegistered
	}
	if !target.Enabled {
		return ErrorTargetDisabled
	}
	workspaceAllowed := contains(target.WorkspaceIDs, workspaceID)
	memberRestricted := len(target.MemberIDs) > 0
	if !workspaceAllowed || (memberRestricted && !contains(target.MemberIDs, memberID)) {
		return ErrorPermissionDenied
	}
	return nil
}

func ResolveTargetIDByHost(host string, targets []Target) (string, error) {
	resolvedID := ""
	for _, target := range targets {
		if target.ID != host && target.RuntimeID != host {
			continue
		}
		if resolvedID != "" && target.ID != resolvedID {
			return "", ErrorHostNotRegistered
		}
		resolvedID = target.ID
	}
	if host == "" || resolvedID == "" {
		return "", ErrorHostNotRegistered
	}
	return resolvedID, nil
}

// SSHTargetAlias recognizes the only remote target form accepted by the
// daemon. The alias is resolved through the daemon's existing OpenSSH config;
// callers never provide hostnames, users, ports, keys or commands.
func SSHTargetAlias(targetID string) (string, bool) {
	alias, ok := strings.CutPrefix(targetID, "ssh:")
	if !ok || len(alias) == 0 || len(alias) > 60 {
		return "", false
	}
	for i, char := range alias {
		isAlphaNumeric := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if (!isAlphaNumeric && char != '_' && char != '-') || (i == 0 && !isAlphaNumeric) {
			return "", false
		}
	}
	return alias, true
}

func PathWithinRoots(roots []string, path string) bool {
	return PathWithinRootsWithPolicy(roots, path, DefaultDenyPolicy())
}

func PathWithinRootsWithPolicy(roots []string, path string, policy DenyPolicy) bool {
	clean, err := NormalizePathWithPolicy(path, policy)
	if err != nil {
		return false
	}
	for _, configured := range roots {
		root, err := filepath.Abs(configured)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), clean)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func EncodeCursor(name string) string { return base64.RawURLEncoding.EncodeToString([]byte(name)) }

func DecodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	name, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(name) == 0 || strings.ContainsAny(string(name), "/\\\x00") || string(name) == "." || string(name) == ".." {
		return "", ErrorRangeInvalid
	}
	return string(name), nil
}

func MetadataFor(targetID, path string, info os.FileInfo) (Metadata, error) {
	if !info.Mode().IsRegular() {
		return Metadata{}, ErrorNotRegularFile
	}
	if info.Size() > MaxFileBytes {
		return Metadata{}, ErrorFileTooLarge
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return Metadata{TargetID: targetID, Path: path, Size: info.Size(), ModTime: info.ModTime().UTC(), ContentType: contentType}, nil
}
