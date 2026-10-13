package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TaskDirectoryScope is one entry of agent_task_queue.local_directory_scope
// (RUYI-669): the task-level declaration of which snapshot resources a task
// touches and which subdirectories it claims. Shape of the whole column:
//
//	[{"resource_id": "<project_resource uuid>", "subdirectories": ["a/b", ...]}]
//
// A NULL column means undeclared: the task runs on the project's primary
// snapshot resource (or its sole one) as a whole-root copy-on-write snapshot
// with no task-lifetime directory lock — isolation comes from the private
// upperdir, and write-set conflicts surface at delivery, not dispatch. A
// present declaration is an exclusive lease: each listed subdirectory of the
// named resource is locked for the task's lifetime, and a later task whose
// declaration intersects waits (waiting_local_directory).
//
// Every scope entry must name at least one subdirectory. "I touch this whole
// resource" is expressed by omitting the scope, not by an empty list — the
// two readings (exclusive whole-root lease vs. lock-free snapshot) must not
// share a spelling.
type TaskDirectoryScope struct {
	ResourceID     string   `json:"resource_id"`
	Subdirectories []string `json:"subdirectories"`
}

// ParseTaskDirectoryScope validates and decodes the scope column. The
// reject-to-guess rules: malformed JSON, unknown keys, trailing data, empty
// resource ids, entries without subdirectories, absolute or ..-escaping
// subdirectory paths, a subdirectory declared twice within one entry, and a
// resource named by more than one entry are all hard failures — callers fail
// the save or the task rather than widening its write set. Both the
// quick-create API (save time) and the daemon claim path (run time) run this
// same parser; the claim-side re-run is the guard against older or non-API
// writers, so it must reject exactly what the save side rejects.
func ParseTaskDirectoryScope(raw []byte) ([]TaskDirectoryScope, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var scopes []TaskDirectoryScope
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&scopes); err != nil {
		return nil, fmt.Errorf("local_directory scope: %w", err)
	}
	if dec.More() {
		return nil, errors.New("local_directory scope: trailing data")
	}
	seenResources := make(map[string]bool, len(scopes))
	for i := range scopes {
		s := &scopes[i]
		s.ResourceID = strings.TrimSpace(s.ResourceID)
		if s.ResourceID == "" {
			return nil, fmt.Errorf("local_directory scope: entry %d: resource_id is required", i+1)
		}
		if seenResources[s.ResourceID] {
			return nil, fmt.Errorf("local_directory scope: resource %s is declared more than once", s.ResourceID)
		}
		seenResources[s.ResourceID] = true
		if len(s.Subdirectories) == 0 {
			return nil, fmt.Errorf("local_directory scope: entry %d: declare at least one subdirectory, or omit the scope to run on the primary resource", i+1)
		}
		seenSubs := make(map[string]bool, len(s.Subdirectories))
		for j, sub := range s.Subdirectories {
			cleaned, err := NormalizeScopeSubdirectory(sub)
			if err != nil {
				return nil, fmt.Errorf("local_directory scope: entry %d subdirectory %d: %w", i+1, j+1, err)
			}
			if seenSubs[cleaned] {
				return nil, fmt.Errorf("local_directory scope: entry %d subdirectory %d: %q is duplicated", i+1, j+1, cleaned)
			}
			seenSubs[cleaned] = true
			s.Subdirectories[j] = cleaned
		}
	}
	return scopes, nil
}

// NormalizeScopeSubdirectory cleans one declared subdirectory:
// slash-normalized, relative, no traversal, no empty segments. Returns the
// canonical form.
func NormalizeScopeSubdirectory(sub string) (string, error) {
	s := strings.Trim(strings.ReplaceAll(strings.TrimSpace(sub), "\\", "/"), "/")
	if s == "" {
		return "", errors.New("is empty")
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." {
			return "", fmt.Errorf("invalid segment %q", seg)
		}
		if seg == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	if strings.ContainsAny(s, "\x00") {
		return "", errors.New("contains a NUL byte")
	}
	return s, nil
}
