package fileaccess

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
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
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return "", ErrorPathNotAllowed
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", ErrorPathNotAllowed
		}
	}
	clean := filepath.Clean(path)
	if sensitivePath(clean) {
		return "", ErrorSensitivePathDenied
	}
	return clean, nil
}

func sensitivePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		name := strings.ToLower(part)
		if name == ".ssh" || name == ".gnupg" || name == ".aws" || name == ".kube" ||
			name == ".env" || strings.HasPrefix(name, ".env.") ||
			name == "id_rsa" || name == "id_ed25519" || strings.HasSuffix(name, ".pem") ||
			strings.Contains(name, "credential") || strings.Contains(name, "secret") || strings.Contains(name, "token") {
			return true
		}
	}
	return false
}

// ResolveRootPath validates lexical and resolved containment before a caller
// opens a path with OpenBeneath. OpenBeneath repeats containment at the kernel
// boundary to close the check/open race.
func ResolveRootPath(roots []string, requested string) (string, error) {
	path, err := NormalizePath(requested)
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
		if sensitivePath(resolved) {
			return "", ErrorSensitivePathDenied
		}
		return resolved, nil
	}
	return "", ErrorPathNotAllowed
}

func ValidateRequest(req Request) error {
	if req.TargetID == "" {
		return ErrorHostNotRegistered
	}
	if _, err := NormalizePath(req.Path); err != nil {
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

func PathWithinRoots(roots []string, path string) bool {
	clean, err := NormalizePath(path)
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
