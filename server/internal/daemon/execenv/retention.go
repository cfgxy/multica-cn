package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const retentionFile = ".environment_retention.json"

// EnvironmentRetention preserves a managed environment for an explicit next
// dependency. It does not renew external VM, device, or service leases.
type EnvironmentRetention struct {
	SchemaVersion int       `json:"schema_version"`
	WorkspaceID   string    `json:"workspace_id"`
	TaskID        string    `json:"task_id"`
	Owner         string    `json:"owner"`
	Reason        string    `json:"reason"`
	Version       string    `json:"version"`
	Evidence      string    `json:"evidence"`
	Until         time.Time `json:"until"`
	RecordedAt    time.Time `json:"recorded_at"`
	ReleasedAt    time.Time `json:"released_at,omitempty"`
}

var retentionVersion = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

// LockEnvironmentRetention coordinates CLI retention changes with the final GC
// mutation. It is separate from the execution lock, which the caller's run owns.
func LockEnvironmentRetention(root string) (func(), error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("retention requires an existing environment directory")
	}
	f, err := openLockFile(filepath.Join(root, ".environment_retention.lock"))
	if err != nil {
		return nil, err
	}
	ok, err := lockFileExclusiveNonBlocking(f)
	if err != nil || !ok {
		_ = f.Close()
		return nil, fmt.Errorf("environment retention is busy; retry after the current update or GC")
	}
	return func() { _ = unlockFile(f); _ = f.Close() }, nil
}

func ReadEnvironmentRetention(root string) (*EnvironmentRetention, error) {
	data, err := os.ReadFile(filepath.Join(root, retentionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r EnvironmentRetention
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("invalid environment retention: %w", err)
	}
	if r.SchemaVersion != 1 || r.TaskID == "" || r.WorkspaceID == "" || r.Owner == "" || r.Reason == "" || r.Evidence == "" || !retentionVersion.MatchString(r.Version) || r.Until.IsZero() || r.RecordedAt.IsZero() {
		return nil, fmt.Errorf("invalid environment retention contract")
	}
	return &r, nil
}

// EnvironmentRetained fails closed on unreadable records and mismatched owners;
// a corrupt hold is never silently interpreted as permission to delete data.
func EnvironmentRetained(root string, now time.Time) (bool, error) {
	r, err := ReadEnvironmentRetention(root)
	if err != nil || r == nil {
		return err != nil, err
	}
	owner, err := ReadEnvRootOwner(root)
	if err != nil || owner.TaskID != r.TaskID || owner.WorkspaceID != r.WorkspaceID {
		return true, fmt.Errorf("environment retention owner does not match provenance")
	}
	return r.ReleasedAt.IsZero() && now.Before(r.Until), nil
}

// SetEnvironmentRetention never creates an environment or changes its owner.
// A caller must identify its active task and workspace explicitly.
func SetEnvironmentRetention(root, taskID, workspaceID string, r EnvironmentRetention, release bool, now time.Time) (*EnvironmentRetention, error) {
	unlock, err := LockEnvironmentRetention(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	owner, err := ReadEnvRootOwner(root)
	if err != nil || taskID == "" || workspaceID == "" || owner.TaskID != taskID || owner.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("retention requires the current task's managed environment")
	}
	if release {
		current, err := ReadEnvironmentRetention(root)
		if err != nil || current == nil {
			return current, err
		}
		if current.TaskID != taskID || current.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("retention belongs to another task")
		}
		r = *current
		if r.ReleasedAt.IsZero() {
			r.ReleasedAt = now.UTC()
		}
	} else {
		r.Owner, r.Reason, r.Evidence = strings.TrimSpace(r.Owner), strings.TrimSpace(r.Reason), strings.TrimSpace(r.Evidence)
		if r.Owner == "" || r.Reason == "" || r.Evidence == "" || !retentionVersion.MatchString(r.Version) || !r.Until.After(now) {
			return nil, fmt.Errorf("owner, reason, evidence, full commit SHA or SHA256, and a future expiry are required")
		}
		r.SchemaVersion, r.TaskID, r.WorkspaceID = 1, taskID, workspaceID
		r.RecordedAt, r.ReleasedAt = now.UTC(), time.Time{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(root, ".retention-*")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(root, retentionFile)); err != nil {
		return nil, err
	}
	return &r, nil
}
