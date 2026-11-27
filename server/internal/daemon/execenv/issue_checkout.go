package execenv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const issueCheckoutRegistry = ".issue-checkouts"

// IssueCheckoutClaim serializes all Runs for an Issue/repository, independently
// of the lifetime of either the Run scratch directory or the checkout itself.
type IssueCheckoutClaim struct {
	Root        string
	lock        *os.File
	legacy      *EnvRootClaim
	record      string
	workspaceID string
	issueID     string
	taskID      string
	runtimeID   string
}

type issueCheckoutBinding struct {
	Root    string
	GitRoot string
	Source  string
	IssueID string
	Owner   branchOwner
}

func (c *IssueCheckoutClaim) Release() {
	c.legacy.Release()
	if c.lock != nil {
		releaseLockFile(c.lock)
		c.lock = nil
	}
}

// MarkPrepared persists checkout identity before a worker may write to it.
func (c *IssueCheckoutClaim) MarkPrepared(logger *slog.Logger) error {
	if _, err := os.Lstat(filepath.Join(c.Root, localWorktreeDirName, ".git")); err != nil {
		return err
	}
	owner, err := ReadEnvRootOwner(c.Root)
	if err == nil && owner.TaskID == "" && owner.WorkspaceID == "" {
		if err := writeEnvRootOwner(c.Root, c.workspaceID, c.taskID); err != nil {
			return err
		}
		owner, err = ReadEnvRootOwner(c.Root)
	}
	if err != nil || owner.TaskID == "" || owner.WorkspaceID != c.workspaceID {
		return errors.New("Issue checkout owner unavailable")
	}
	meta, err := ReadGCMeta(c.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if meta == nil {
		meta = &GCMeta{Kind: GCKindIssue, WorkspaceID: c.workspaceID, IssueID: c.issueID, TaskID: owner.TaskID}
	}
	if meta.Kind != GCKindIssue || meta.WorkspaceID != c.workspaceID || meta.IssueID != c.issueID {
		return errors.New("Issue checkout metadata changed")
	}
	meta.LatestTaskID = c.taskID
	meta.CompletedAt = time.Now().UTC()
	meta.RuntimeID = c.runtimeID
	if err := WriteGCMeta(c.Root, *meta, logger); err != nil {
		return err
	}
	return writeIssueCheckoutFile(c.record+".ready", nil)
}

// Keep the marker after an unconfirmed exit or a daemon crash. An operator
// must prove the worker has stopped before clearing the marker.
func (c *IssueCheckoutClaim) MarkWorker() error {
	// After successful preparation/restore, an old archive is no longer a
	// recovery point for mutations by this Run. Invalidate it durably first.
	if _, err := os.Lstat(c.record + ".archive"); err == nil {
		if err := writeIssueCheckoutFile(c.record+".archive", nil); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(c.record + ".worker"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("Issue checkout worker marker already exists or cannot be checked")
	}
	return writeIssueCheckoutFile(c.record+".worker", nil)
}

func (c *IssueCheckoutClaim) ClearWorker() error { return os.Remove(c.record + ".worker") }

func ClaimIssueCheckout(ctx context.Context, workspacesRoot, issueID string, params LocalWorktreeParams, onWait func()) (*IssueCheckoutClaim, error) {
	return claimIssueCheckout(ctx, workspacesRoot, issueID, params, onWait, nil)
}

func ClaimRestorableIssueCheckout(ctx context.Context, workspacesRoot, issueID string, params LocalWorktreeParams, onWait func(), profile, backend string, logger *slog.Logger) (*IssueCheckoutClaim, error) {
	claim, err := claimIssueCheckout(ctx, workspacesRoot, issueID, params, onWait, func(ws, root, record string) error {
		data, err := os.ReadFile(record + ".archive")
		if err != nil {
			return err
		}
		id := string(data)
		if id == "" {
			return errors.New("no current Issue checkout archive")
		}
		manifest, err := ReadEnvironmentArchive(ws, id)
		if err != nil {
			return err
		}
		if filepath.Join(ws, filepath.FromSlash(manifest.RelativeRoot)) != root || manifest.Owner.WorkspaceID != params.WorkspaceID || manifest.Metadata == nil || manifest.Metadata.IssueID != issueID || manifest.Metadata.Kind != GCKindIssue {
			return errors.New("Issue checkout archive identity changed")
		}
		_, err = RestoreEnvironmentArchive(ctx, ws, id, profile, backend)
		return err
	})
	if err != nil {
		return nil, err
	}
	git, err := os.Lstat(filepath.Join(claim.Root, localWorktreeDirName, ".git"))
	if err == nil && git.IsDir() {
		restored, err := EnvironmentRestoredAt(claim.Root)
		if err != nil || restored.IsZero() {
			claim.Release()
			return nil, errors.New("unexpected standalone Issue checkout")
		}
		previous, err := ReadRetainedLocalWorktree(claim.Root)
		if err == nil {
			err = ReattachRestoredLocalWorktree(ctx, previous, params, logger)
		}
		if err != nil {
			claim.Release()
			return nil, err
		}
	}
	return claim, nil
}

func claimIssueCheckout(ctx context.Context, workspacesRoot, issueID string, params LocalWorktreeParams, onWait func(), restore func(ws, root, record string) error) (*IssueCheckoutClaim, error) {
	if issueID == "" || !params.owner().valid() || params.IssueID != issueID {
		return nil, errors.New("incomplete Issue checkout scope")
	}
	ws, err := filepath.EvalSymlinks(workspacesRoot)
	if err != nil {
		return nil, err
	}
	gitRoot, err := resolveGitRoot(params.LocalPath)
	if err != nil {
		return nil, err
	}
	source, err := filepath.EvalSymlinks(params.LocalPath)
	if err != nil {
		return nil, err
	}
	common, err := runGitTrimmed(gitRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(params.WorkspaceID+"\x00"+issueID+"\x00"+common)))
	registry := filepath.Join(ws, issueCheckoutRegistry)
	if err := os.Mkdir(registry, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(registry)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
		return nil, errors.New("invalid Issue checkout registry")
	}
	record := filepath.Join(registry, key+".json")
	lock, err := openLockFile(filepath.Join(registry, key+".lock"))
	if err != nil {
		return nil, err
	}
	result := &IssueCheckoutClaim{lock: lock, record: record, workspaceID: params.WorkspaceID, issueID: issueID, taskID: params.TaskID, runtimeID: params.RuntimeID}
	success := false
	defer func() {
		if !success {
			result.Release()
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		locked, err := lockFileExclusiveNonBlocking(lock)
		if err != nil {
			return nil, err
		}
		if locked {
			break
		}
		if onWait != nil {
			onWait()
			onWait = nil
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := os.Lstat(record + ".worker"); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Issue checkout may still have an active worker; confirm its exit before recovery")
	}
	stableRoot := filepath.Join(registry, key)
	root := stableRoot
	data, err := os.ReadFile(record)
	if err == nil {
		var binding issueCheckoutBinding
		if json.Unmarshal(data, &binding) != nil || binding.GitRoot != gitRoot || binding.Source != source || binding.IssueID != issueID || !binding.Owner.valid() || !params.owner().matches(binding.Owner) {
			return nil, errors.New("Issue checkout scope changed; refusing to fork or rebind")
		}
		root = binding.Root
		rel, err := filepath.Rel(ws, root)
		if err != nil || !validArchivedRoot(rel) || (filepath.Dir(root) == registry && root != stableRoot) {
			return nil, errors.New("invalid Issue checkout binding path")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		legacy, err := findLegacyIssueCheckout(ws, issueID, params, gitRoot)
		if err != nil {
			return nil, err
		}
		if legacy != "" {
			root = legacy
		}
		data, err := json.Marshal(issueCheckoutBinding{Root: root, GitRoot: gitRoot, Source: source, IssueID: issueID, Owner: params.owner()})
		if err != nil {
			return nil, err
		}
		if err := writeIssueCheckoutFile(record, data); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	_, readyErr := os.Lstat(record + ".ready")
	if readyErr != nil && !errors.Is(readyErr, os.ErrNotExist) {
		return nil, readyErr
	}
	if readyErr == nil || root != stableRoot {
		if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) && restore != nil {
			if err := restore(ws, root, record); err != nil {
				return nil, fmt.Errorf("restore Issue checkout: %w", err)
			}
		}
		if _, err := os.Lstat(filepath.Join(root, localWorktreeDirName, ".git")); err != nil {
			return nil, fmt.Errorf("established Issue checkout is unavailable; recovery required: %w", err)
		}
	} else if err := os.Mkdir(root, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := validateIssueCheckoutRoot(ws, root); err != nil {
		return nil, err
	}
	if root != stableRoot {
		handle, err := os.OpenRoot(ws)
		if err != nil {
			return nil, err
		}
		defer handle.Close()
		rel, err := filepath.Rel(ws, root)
		if err != nil {
			return nil, err
		}
		for {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			claim, info, err := LockEnvRootForReuse(handle, rel, root)
			if err == nil && claim != nil {
				result.legacy = claim
				current, err := handle.Lstat(rel)
				if err != nil || !os.SameFile(info, current) {
					return nil, errors.New("legacy Issue checkout changed during claim")
				}
				break
			}
			if !errors.Is(err, ErrEnvRootBusy) {
				return nil, fmt.Errorf("claim legacy Issue checkout: %v", err)
			}
			if onWait != nil {
				onWait()
				onWait = nil
			}
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	result.Root = root
	if root != stableRoot {
		existing, err := ReadRetainedLocalWorktree(root)
		if err != nil || !LocalWorktreeReuseMatches(existing, params) {
			return nil, errors.New("legacy Issue checkout changed ownership during claim")
		}
	}
	success = true
	return result, nil
}

func validateIssueCheckoutRoot(ws, root string) error {
	rel, err := filepath.Rel(ws, root)
	if err != nil || !filepath.IsLocal(rel) || len(strings.Split(rel, string(filepath.Separator))) != 2 {
		return errors.New("invalid Issue checkout root")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return errors.New("Issue checkout root missing or redirected; recovery required")
	}
	return nil
}

func findLegacyIssueCheckout(ws, issueID string, params LocalWorktreeParams, gitRoot string) (string, error) {
	entries, err := os.ReadDir(ws)
	if err != nil {
		return "", err
	}
	var found string
	for _, workspace := range entries {
		if !workspace.IsDir() || strings.HasPrefix(workspace.Name(), ".") {
			continue
		}
		roots, err := os.ReadDir(filepath.Join(ws, workspace.Name()))
		if err != nil {
			return "", err
		}
		for _, entry := range roots {
			if !entry.IsDir() {
				continue
			}
			root := filepath.Join(ws, workspace.Name(), entry.Name())
			meta, err := ReadGCMeta(root)
			if err != nil || meta.WorkspaceID != params.WorkspaceID || meta.IssueID != issueID {
				continue
			}
			if _, err := os.Lstat(filepath.Join(root, localWorktreeDirName, ".git")); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return "", err
			}
			previous, err := ReadRetainedLocalWorktree(root)
			if err != nil {
				return "", fmt.Errorf("existing Issue checkout %s requires manual recovery: %w", root, err)
			}
			if previous.GitRoot != gitRoot {
				continue
			}
			if !LocalWorktreeReuseMatches(previous, params) || found != "" {
				return "", errors.New("conflicting legacy Issue checkouts; refusing to create another")
			}
			found = root
		}
	}
	return found, nil
}

// RecycleIssueCheckouts uses the same registry lock as execution, then holds
// the Issue reservation and (for adopted roots) the pre-registry lease.
func RecycleIssueCheckouts(ctx context.Context, ws, profile, backend string, eligible func(GCMeta) bool, fence func(context.Context, string, string, string, bool) error, logger *slog.Logger) {
	ws, err := filepath.EvalSymlinks(ws)
	if err != nil {
		return
	}
	registry := filepath.Join(ws, issueCheckoutRegistry)
	info, err := os.Lstat(registry)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
		return
	}
	entries, err := os.ReadDir(registry)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") || !validEnvironmentArchiveID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		record := filepath.Join(registry, name)
		if err := recycleIssueCheckout(ctx, ws, record, profile, backend, eligible, fence); err != nil {
			logger.Debug("Issue checkout recycling deferred", "error", err)
		}
	}
}

func recycleIssueCheckout(ctx context.Context, ws, record, profile, backend string, eligible func(GCMeta) bool, fence func(context.Context, string, string, string, bool) error) error {
	stableRoot := strings.TrimSuffix(record, ".json")
	lock, err := openLockFile(stableRoot + ".lock")
	if err != nil {
		return err
	}
	locked, err := lockFileExclusiveNonBlocking(lock)
	if !locked || err != nil {
		lock.Close()
		return err
	}
	defer releaseLockFile(lock)
	if _, err := os.Lstat(record + ".worker"); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	data, err := os.ReadFile(record)
	if err != nil {
		return err
	}
	var b issueCheckoutBinding
	if err := json.Unmarshal(data, &b); err != nil || !b.Owner.valid() {
		return errors.New("invalid Issue checkout binding")
	}
	// The journal contains an opaque token and scope, and is durable before
	// making the request. A restart ends the old attempt rather than resuming it.
	journal := record + ".reclaim"
	var attempt struct{ Workspace, Issue, Profile, Backend, Token string }
	data, err = os.ReadFile(journal)
	if err == nil {
		if json.Unmarshal(data, &attempt) != nil || attempt.Workspace != b.Owner.WorkspaceID || attempt.Issue != b.IssueID || attempt.Profile != profile || attempt.Backend != backend || !validEnvironmentArchiveID(attempt.Token) {
			return errors.New("Issue reclamation journal belongs to a different scope")
		}
		if err := fence(ctx, attempt.Workspace, attempt.Issue, attempt.Token, true); err != nil {
			return err
		}
		if err := os.Remove(journal); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(record + ".ready"); err != nil {
		return nil
	}
	if err := validateIssueCheckoutRoot(ws, b.Root); err != nil {
		return nil
	}
	rel, err := filepath.Rel(ws, b.Root)
	if err != nil || !validArchivedRoot(rel) || (filepath.Dir(b.Root) == filepath.Dir(stableRoot) && b.Root != stableRoot) {
		return errors.New("invalid Issue archive root")
	}
	if b.Root != stableRoot {
		h, err := os.OpenRoot(ws)
		if err != nil {
			return err
		}
		defer h.Close()
		claim, info, err := LockEnvRootForReuse(h, rel, b.Root)
		if err != nil {
			return err
		}
		defer claim.Release()
		current, err := h.Lstat(rel)
		if err != nil || !os.SameFile(info, current) {
			return errors.New("legacy Issue checkout switched")
		}
	}
	meta, err := ReadGCMeta(b.Root)
	if err != nil || meta.Kind != GCKindIssue || meta.WorkspaceID != b.Owner.WorkspaceID || meta.IssueID == "" || meta.IssueID != b.IssueID || !eligible(*meta) {
		return nil
	}
	previous, err := ReadRetainedLocalWorktree(b.Root)
	if err != nil || !b.Owner.matches(previous.owner) || (previous.IssueID != "" && previous.IssueID != b.IssueID) || previous.GitRoot != b.GitRoot {
		return errors.New("Issue checkout recovery binding unavailable")
	}
	unlock, err := LockEnvironmentArchiveRepositories(ctx, b.Root)
	if err != nil {
		return err
	}
	defer unlock()
	revision, err := EnvironmentArchiveRevision(ctx, b.Root)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(record+"\x00"+profile+"\x00"+backend+"\x00"+revision)))
	if _, err := CaptureEnvironmentArchive(ctx, EnvironmentArchiveRequest{WorkspacesRoot: ws, EnvRoot: b.Root, ID: id, Revision: revision, Profile: profile, BackendURL: backend}); err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	attempt.Workspace, attempt.Issue, attempt.Profile, attempt.Backend, attempt.Token = meta.WorkspaceID, meta.IssueID, profile, backend, hex.EncodeToString(token)
	data, err = json.Marshal(attempt)
	if err != nil {
		return err
	}
	if err := writeIssueCheckoutFile(journal, data); err != nil {
		return err
	}
	beginErr := fence(ctx, attempt.Workspace, attempt.Issue, attempt.Token, false)
	var reclaimErr error
	if beginErr == nil {
		reclaimErr = writeIssueCheckoutFile(record+".archive", []byte(id))
		if reclaimErr == nil {
			reclaimErr = ReclaimEnvironmentArchive(ctx, ws, id)
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	finishErr := fence(finishCtx, attempt.Workspace, attempt.Issue, attempt.Token, true)
	if finishErr == nil {
		finishErr = os.Remove(journal)
	}
	return errors.Join(beginErr, reclaimErr, finishErr)
}

func writeIssueCheckoutFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".issue-checkout-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if err := errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return publishIssueCheckoutFile(tmp.Name(), path)
}
