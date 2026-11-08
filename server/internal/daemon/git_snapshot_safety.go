package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// gitSnapshotAudit 描述 worker 启动前解析出的 Git 读写边界。审计只读；失败
// 时必须回退既有 worktree，绝不尝试修复用户的 Git metadata。
type gitSnapshotAudit struct {
	HasGit       bool
	Safe         bool
	Reason       string
	Repositories []gitSnapshotRepository
	Dependencies []string
}

// gitSnapshotRepository 是一个工作树及其 Git 写入依赖。Dependencies 覆盖
// git dir、common dir 和可达的 alternate object database。
type gitSnapshotRepository struct {
	Worktree     string
	GitDir       string
	CommonDir    string
	Dependencies []string
}

// auditSnapshotGitScope 递归发现资源 scope 内的工作树，并解析每个工作树的
// Git metadata。工作树本身必须仍在用户声明的 scope 中；metadata 可位于外部，
// 仅在可解析为现存目录时作为 daemon 内部 overlay dependency 纳入计划。
func auditSnapshotGitScope(plan *snapshotPlan) gitSnapshotAudit {
	if plan == nil {
		return gitSnapshotAudit{Safe: true}
	}
	units := plan.Units
	if len(units) == 0 && plan.Primary != nil {
		units = []snapshotUnit{*plan.Primary}
	}

	covered := make([]string, 0, len(units))
	for _, unit := range units {
		real, err := canonicalGitDirectory(unit.AbsPath)
		if err != nil {
			return gitSnapshotAudit{Reason: fmt.Sprintf("cannot resolve snapshot scope %q: %v", unit.AbsPath, err)}
		}
		covered = append(covered, real)
	}

	repositories := make(map[string]struct{})
	for _, unit := range units {
		if err := filepath.WalkDir(unit.AbsPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Name() != ".git" {
				return nil
			}
			repositories[filepath.Dir(path)] = struct{}{}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}); err != nil {
			return gitSnapshotAudit{Reason: fmt.Sprintf("cannot enumerate Git metadata below %q: %v", unit.AbsPath, err)}
		}
	}

	// 一个 unit 可以指向仓库子目录，因此其 .git 不一定在 WalkDir 根目录下。
	for _, unit := range units {
		if top, ok := gitToplevel(unit.AbsPath); ok {
			repositories[top] = struct{}{}
		}
	}
	if len(repositories) == 0 {
		return gitSnapshotAudit{Safe: true}
	}

	repoPaths := make([]string, 0, len(repositories))
	for repo := range repositories {
		repoPaths = append(repoPaths, repo)
	}
	sort.Strings(repoPaths)

	audit := gitSnapshotAudit{HasGit: true}
	for _, repo := range repoPaths {
		top, err := gitPath(repo, "--show-toplevel")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git worktree %q: %v", repo, err)}
		}
		gitDir, err := gitPath(repo, "--absolute-git-dir")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git directory for %q: %v", repo, err)}
		}
		commonDir, err := gitPath(repo, "--git-common-dir")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve common Git directory for %q: %v", repo, err)}
		}
		top, err = canonicalGitDirectory(top)
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git worktree %q: %v", repo, err)}
		}
		gitDir, err = canonicalGitDirectory(gitDir)
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git directory for %q: %v", repo, err)}
		}
		commonDir, err = canonicalGitDirectory(commonDir)
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve common Git directory for %q: %v", repo, err)}
		}
		if !withinAnyScope(top, covered) && !withinAnyScopeForAnyScope(covered, top) {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("Git worktree %q is outside the declared snapshot scope", top)}
		}

		dependencies, err := gitSnapshotDependencies(repo, gitDir, commonDir)
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git metadata dependencies for %q: %v", repo, err)}
		}
		audit.Repositories = append(audit.Repositories, gitSnapshotRepository{
			Worktree: top, GitDir: gitDir, CommonDir: commonDir, Dependencies: dependencies,
		})
		audit.Dependencies = append(audit.Dependencies, dependencies...)
	}
	commonDirs := gitSnapshotCommonDirs(audit.Repositories)
	for _, repository := range audit.Repositories {
		if err := auditGitMetadataRedirects(repository.Worktree, repository.GitDir, repository.CommonDir, repository.Dependencies, commonDirs); err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("unsafe Git metadata for %q: %v", repository.Worktree, err)}
		}
	}
	audit.Dependencies = compactContainedPaths(audit.Dependencies)
	audit.Safe = true
	return audit
}

func gitSnapshotCommonDirs(repositories []gitSnapshotRepository) []string {
	commonDirs := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		commonDirs = append(commonDirs, repository.CommonDir)
	}
	return uniqueGitPaths(commonDirs)
}

func uniqueGitPaths(paths []string) []string {
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		unique[filepath.Clean(path)] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for path := range unique {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// gitSnapshotDeliveryEligible 比只读审计更窄：当前 artifact contract 只交付
// 一个主仓的 bundle。根资源保留已支持的 linked worktree 路径；声明子目录仅
// 支持仓根带普通 .git 目录的单仓，metadata 作为内部 overlay unit 单独挂载。
func gitSnapshotDeliveryEligible(plan *snapshotPlan, enabled bool) bool {
	return gitSnapshotDeliveryEligibleFromAudit(plan, enabled, auditSnapshotGitScope(plan))
}

func gitSnapshotDeliveryEligibleFromAudit(plan *snapshotPlan, enabled bool, audit gitSnapshotAudit) bool {
	_, ok := gitSnapshotDeliveryRepository(plan, enabled, audit)
	return ok
}

func gitSnapshotDeliveryRepository(plan *snapshotPlan, enabled bool, audit gitSnapshotAudit) (gitSnapshotRepository, bool) {
	if !enabled || plan == nil || plan.Primary == nil || len(plan.Units) != 1 {
		return gitSnapshotRepository{}, false
	}
	unit := plan.Units[0]
	if filepath.Clean(unit.AbsPath) != filepath.Clean(plan.Primary.AbsPath) {
		return gitSnapshotRepository{}, false
	}
	if !audit.HasGit || !audit.Safe || len(audit.Repositories) != 1 {
		return gitSnapshotRepository{}, false
	}
	repository := audit.Repositories[0]
	if !withinAnyScope(unit.AbsPath, []string{repository.Worktree}) {
		return gitSnapshotRepository{}, false
	}
	if _, err := os.Stat(filepath.Join(repository.Worktree, ".gitmodules")); err == nil {
		// 尚未初始化的 submodule 没有可递归发现的子工作树，但它的对象库也
		// 不会由父仓 bundle 携带；必须等待完整多仓 artifact contract。
		return gitSnapshotRepository{}, false
	} else if !os.IsNotExist(err) {
		return gitSnapshotRepository{}, false
	}
	if unit.Subdir == "" {
		return repository, filepath.Clean(repository.Worktree) == filepath.Clean(unit.AbsPath)
	}

	// A declared Git subdirectory mounts only the repository's own .git as
	// internal metadata. Linked worktrees, external common directories, and
	// symlinked metadata need a broader artifact contract, so retain worktree
	// fallback for them.
	gitPath := filepath.Join(repository.Worktree, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || !info.IsDir() {
		return gitSnapshotRepository{}, false
	}
	if filepath.Clean(repository.GitDir) != filepath.Clean(gitPath) || filepath.Clean(repository.CommonDir) != filepath.Clean(gitPath) {
		return gitSnapshotRepository{}, false
	}
	return repository, true
}

func gitPath(repo string, args ...string) (string, error) {
	argv := append([]string{"-C", repo, "rev-parse"}, args...)
	cmd := exec.Command("git", argv...)
	cmd.Env = execenv.SnapshotGitEnvironment()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("Git returned an empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo, path)
	}
	return filepath.Clean(path), nil
}

func gitSnapshotDependencies(repo, gitDir, commonDir string) ([]string, error) {
	objectDir, err := gitPath(repo, "--git-path", "objects")
	if err != nil {
		return nil, err
	}
	objectDir, err = canonicalGitDirectory(objectDir)
	if err != nil {
		return nil, err
	}

	dependencies := []string{gitDir, commonDir, objectDir}
	queue := []string{objectDir}
	seen := map[string]bool{objectDir: true}
	for len(queue) > 0 {
		objects := queue[0]
		queue = queue[1:]
		alternates, err := os.ReadFile(filepath.Join(objects, "info", "alternates"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, alternate := range strings.Split(string(alternates), "\n") {
			alternate = strings.TrimSpace(alternate)
			if alternate == "" {
				continue
			}
			if strings.ContainsRune(alternate, 0) {
				return nil, fmt.Errorf("alternate object path contains NUL")
			}
			if !filepath.IsAbs(alternate) {
				alternate = filepath.Join(objects, alternate)
			}
			alternate, err = canonicalGitDirectory(alternate)
			if err != nil {
				return nil, err
			}
			if seen[alternate] {
				continue
			}
			seen[alternate] = true
			dependencies = append(dependencies, alternate)
			queue = append(queue, alternate)
		}
	}
	return compactContainedPaths(dependencies), nil
}

// auditGitMetadataRedirects rejects metadata links that would make Git write
// outside this repository's Git directory. alternate object databases remain
// read dependencies only, even when snapshot delivery materializes them.
func auditGitMetadataRedirects(worktree, gitDir, commonDir string, dependencies, commonDirs []string) error {
	writableMetadata := compactContainedPaths([]string{gitDir, commonDir})
	return auditGitMetadataRedirectsWithin(worktree, gitDir, commonDir, dependencies, writableMetadata, dependencies, commonDirs, true)
}

// auditGitMetadataRedirectsWithin audits Git's dependency graph against an
// explicit set of writable metadata paths. alternateAllowed is intentionally
// separate: alternate object databases are read dependencies, but they must
// not authorise refs, indexes, or other metadata redirects to write there.
func auditGitMetadataRedirectsWithin(worktree, gitDir, commonDir string, dependencies, allowed, alternateAllowed, commonDirs []string, allowLinkedWorktreeBacklink bool) error {
	allowed = compactContainedPaths(allowed)
	alternateAllowed = compactContainedPaths(alternateAllowed)
	commonDirs = uniqueGitPaths(commonDirs)
	forbidden, forbiddenRoots, err := siblingGitWorktreeAdminDirs(commonDirs, gitDir)
	if err != nil {
		return err
	}
	worktreesRoot := filepath.Join(commonDir, "worktrees")
	currentLinkedGitDir := filepath.Dir(gitDir) == worktreesRoot

	if err := auditGitMetadataEntry(filepath.Join(worktree, ".git"), worktree, allowed, alternateAllowed, forbidden, forbiddenRoots, true, allowLinkedWorktreeBacklink); err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if err := filepath.WalkDir(dependency, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == dependency {
				return nil
			}
			if entry.IsDir() {
				if isKnownGitWorktreesRoot(path, commonDirs) {
					if path != worktreesRoot || !currentLinkedGitDir {
						// Sibling worktree admin records are not dependencies of the current
						// repository. This also applies to repositories nested below the
						// current common directory, such as initialized submodules.
						return filepath.SkipDir
					}
				}
				if filepath.Dir(path) == worktreesRoot && path != gitDir {
					return filepath.SkipDir
				}
			}
			entryAllowed := allowed
			if entry.Type()&os.ModeSymlink != 0 && isGitHooksDirectory(path, gitDir, commonDir) {
				// Hooks are executed, not written by Git. A shared hook directory in
				// this worktree is materialized with the snapshot, unlike writable
				// metadata redirects such as refs or index.
				entryAllowed = compactContainedPaths(append(append([]string{}, allowed...), worktree))
			}
			return auditGitMetadataEntry(path, worktree, entryAllowed, alternateAllowed, forbidden, forbiddenRoots, false, allowLinkedWorktreeBacklink)
		}); err != nil {
			return err
		}
	}
	return nil
}

func isGitHooksDirectory(path, gitDir, commonDir string) bool {
	path = filepath.Clean(path)
	return path == filepath.Clean(filepath.Join(gitDir, "hooks")) ||
		path == filepath.Clean(filepath.Join(commonDir, "hooks"))
}

func siblingGitWorktreeAdminDirs(commonDirs []string, currentGitDir string) ([]string, []string, error) {
	var siblings []string
	var roots []string
	worktreesRoots := make([]string, 0, len(commonDirs)+1)
	for _, commonDir := range commonDirs {
		worktreesRoots = append(worktreesRoots, filepath.Join(commonDir, "worktrees"))
	}
	if filepath.Base(filepath.Dir(currentGitDir)) == "worktrees" {
		worktreesRoots = append(worktreesRoots, filepath.Dir(currentGitDir))
	}
	for _, worktreesRoot := range uniqueGitPaths(worktreesRoots) {
		entries, err := os.ReadDir(worktreesRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, err
		}
		hasSibling := false
		for _, entry := range entries {
			path := filepath.Join(worktreesRoot, entry.Name())
			if filepath.Clean(path) != filepath.Clean(currentGitDir) {
				hasSibling = true
				siblings = append(siblings, path)
			}
		}
		if hasSibling {
			roots = append(roots, worktreesRoot)
		}
	}
	return compactContainedPaths(siblings), uniqueGitPaths(roots), nil
}

func isKnownGitWorktreesRoot(path string, commonDirs []string) bool {
	if filepath.Base(path) != "worktrees" {
		return false
	}
	parent := filepath.Clean(filepath.Dir(path))
	for _, commonDir := range commonDirs {
		if parent == filepath.Clean(commonDir) {
			return true
		}
	}
	return false
}

// auditGitMetadataEntry checks Git's filesystem-level redirects without
// following them during traversal. A link may target a regular file (index) or
// directory (refs), so containment is evaluated on the resolved target rather
// than requiring a directory.
func auditGitMetadataEntry(path, worktree string, allowed, alternateAllowed, forbidden, forbiddenRoots []string, gitEntry, allowLinkedWorktreeBacklink bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return auditGitMetadataRedirect(path, allowed, forbidden, forbiddenRoots, false)
	}
	if gitEntry {
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Git entry %q is neither a directory nor a redirect file", path)
		}
		return auditGitMetadataRedirect(path, allowed, forbidden, forbiddenRoots, true)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if strings.Contains(filepath.ToSlash(path), "/refs/") {
		return auditGitSymbolicRef(path)
	}
	switch filepath.Base(path) {
	case "commondir":
		return auditGitMetadataRedirect(path, allowed, forbidden, forbiddenRoots, false)
	case "gitdir":
		if allowLinkedWorktreeBacklink {
			return auditGitLinkedWorktreeBacklink(path, worktree, allowed, forbidden, forbiddenRoots)
		}
		return auditGitMetadataRedirect(path, allowed, forbidden, forbiddenRoots, false)
	case "alternates":
		return auditGitAlternateRedirects(path, alternateAllowed)
	default:
		return auditGitSymbolicRef(path)
	}
}

// auditGitLinkedWorktreeBacklink permits only Git's documented linked-worktree
// admin backlink: common/worktrees/<name>/gitdir -> <worktree>/.git. The
// destination file is then parsed as a Git redirect and must terminate in a
// Git metadata dependency. No other worktree path is safe for the legacy
// worktree backend.
func auditGitLinkedWorktreeBacklink(path, worktree string, allowed, forbidden, forbiddenRoots []string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(string(raw))
	if target == "" || strings.ContainsRune(target, 0) {
		return fmt.Errorf("invalid Git metadata redirect %q", path)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	direct := filepath.Clean(target)
	if direct != filepath.Clean(filepath.Join(worktree, ".git")) {
		return fmt.Errorf("metadata redirect %q targets outside Git dependencies at %q", path, direct)
	}
	return auditGitMetadataRedirect(direct, allowed, forbidden, forbiddenRoots, true)
}

// auditGitMetadataRedirect resolves symlink and Git redirect-file targets and
// ensures every target remains inside the worktree or an overlay dependency.
// gitdir-prefixed is the format for a worktree's .git file; commondir and the
// administrative gitdir file contain a bare path.
func auditGitMetadataRedirect(path string, allowed, forbidden, forbiddenRoots []string, gitdirPrefixed bool) error {
	var target string
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read metadata symlink %q: %w", path, err)
		}
	} else {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		target = strings.TrimSpace(string(raw))
		if gitdirPrefixed {
			var ok bool
			target, ok = strings.CutPrefix(target, "gitdir: ")
			if !ok {
				return fmt.Errorf("invalid Git redirect file %q", path)
			}
			target = strings.TrimSpace(target)
		}
		if target == "" || strings.ContainsRune(target, 0) {
			return fmt.Errorf("invalid Git metadata redirect %q", path)
		}
	}
	return auditGitMetadataRedirectTarget(path, filepath.Dir(path), target, allowed, forbidden, forbiddenRoots)
}

// auditGitAlternateRedirects checks every alternate object database before the
// worker can use it. Relative alternates are relative to the object directory,
// rather than the info directory holding the file.
func auditGitAlternateRedirects(path string, allowed []string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	objectDir := filepath.Dir(filepath.Dir(path))
	for _, target := range strings.Split(string(raw), "\n") {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if strings.ContainsRune(target, 0) {
			return fmt.Errorf("alternate object path in %q contains NUL", path)
		}
		if err := auditGitMetadataRedirectTarget(path, objectDir, target, allowed, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// auditGitMetadataRedirectTarget rejects both a direct target outside the
// materialized set and a target whose later symlink resolution escapes it. The
// direct check prevents a host-side intermediate symlink from escaping the
// mount set and then re-entering it at a permitted destination.
func auditGitMetadataRedirectTarget(path, base, target string, allowed, forbidden, forbiddenRoots []string) error {
	if target == "" || strings.ContainsRune(target, 0) {
		return fmt.Errorf("invalid Git metadata redirect %q", path)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	direct := filepath.Clean(target)
	if containsCleanPath(direct, forbiddenRoots) {
		return fmt.Errorf("metadata redirect %q targets sibling Git worktree admin root at %q", path, direct)
	}
	if withinAnyScope(direct, forbidden) {
		return fmt.Errorf("metadata redirect %q targets sibling Git worktree admin state at %q", path, direct)
	}
	if !withinAnyScope(direct, allowed) {
		return fmt.Errorf("metadata redirect %q targets outside materialized dependencies at %q", path, direct)
	}
	resolved, err := filepath.EvalSymlinks(direct)
	if err != nil {
		return fmt.Errorf("resolve metadata redirect %q: %w", path, err)
	}
	resolved = filepath.Clean(resolved)
	if containsCleanPath(resolved, forbiddenRoots) {
		return fmt.Errorf("metadata redirect %q resolves to sibling Git worktree admin root at %q", path, resolved)
	}
	if withinAnyScope(resolved, forbidden) {
		return fmt.Errorf("metadata redirect %q resolves to sibling Git worktree admin state at %q", path, resolved)
	}
	if !withinAnyScope(resolved, allowed) {
		return fmt.Errorf("metadata redirect %q resolves outside materialized dependencies to %q", path, resolved)
	}
	return nil
}

func containsCleanPath(path string, candidates []string) bool {
	for _, candidate := range candidates {
		if filepath.Clean(path) == filepath.Clean(candidate) {
			return true
		}
	}
	return false
}

// auditGitSymbolicRef validates symbolic refs stored as ordinary files. Their
// target syntax is repository-relative, so an absolute path or traversal would
// turn a ref update into an unmaterialized filesystem write.
func auditGitSymbolicRef(path string) error {
	base := filepath.Base(path)
	if base != "HEAD" && !strings.Contains(filepath.ToSlash(path), "/refs/") {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	target, ok := strings.CutPrefix(string(raw), "ref: ")
	if !ok {
		return nil
	}
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsRune(target, 0) || filepath.IsAbs(target) {
		return fmt.Errorf("invalid symbolic Git ref %q", path)
	}
	clean := filepath.ToSlash(filepath.Clean(target))
	if !strings.HasPrefix(clean, "refs/") || clean == "refs" || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("symbolic Git ref %q escapes refs namespace", path)
	}
	return nil
}

func canonicalGitDirectory(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Clean(real), nil
}

func compactContainedPaths(paths []string) []string {
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		unique[filepath.Clean(path)] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for path := range unique {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool {
		depthI := strings.Count(filepath.Clean(ordered[i]), string(filepath.Separator))
		depthJ := strings.Count(filepath.Clean(ordered[j]), string(filepath.Separator))
		if depthI != depthJ {
			return depthI < depthJ
		}
		return ordered[i] < ordered[j]
	})
	kept := make([]string, 0, len(ordered))
	for _, path := range ordered {
		if !withinAnyScope(path, kept) {
			kept = append(kept, path)
		}
	}
	return kept
}

func withinAnyScope(path string, scopes []string) bool {
	for _, scope := range scopes {
		rel, err := filepath.Rel(scope, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// withinAnyScopeForAnyScope reports whether any declared scope is nested in
// parent. A Git subdirectory has a repository root above the business scope;
// that root is read-only from the daemon's perspective while its .git is
// separately materialized as an internal metadata unit.
func withinAnyScopeForAnyScope(scopes []string, parent string) bool {
	for _, scope := range scopes {
		if withinAnyScope(scope, []string{parent}) {
			return true
		}
	}
	return false
}
