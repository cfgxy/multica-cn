package daemon

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

const (
	gitSnapshotAuditTimeout       = 10 * time.Second
	gitSnapshotAuditMaxEntries    = 100000
	gitSnapshotAuditMaxConfigFile = 4 << 20
	gitSnapshotAuditMaxConfigSrc  = 1024
	gitSnapshotAuditMaxAlternates = 1024
)

type gitSnapshotAuditBudget struct {
	ctx            context.Context
	entries        int
	configBytes    int64
	configSources  int
	alternatePaths int
}

func newGitSnapshotAuditBudget(ctx context.Context) *gitSnapshotAuditBudget {
	return &gitSnapshotAuditBudget{ctx: ctx}
}

func (b *gitSnapshotAuditBudget) check() error {
	select {
	case <-b.ctx.Done():
		return fmt.Errorf("Git audit exceeded its time budget: %w", b.ctx.Err())
	default:
		return nil
	}
}

func (b *gitSnapshotAuditBudget) visit() error {
	if err := b.check(); err != nil {
		return err
	}
	b.entries++
	if b.entries > gitSnapshotAuditMaxEntries {
		return fmt.Errorf("Git audit exceeded the directory entry budget")
	}
	return nil
}

func (b *gitSnapshotAuditBudget) readConfig(path string) ([]byte, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > gitSnapshotAuditMaxConfigFile {
		return nil, fmt.Errorf("Git configuration file %q exceeds the size budget", path)
	}
	b.configBytes += info.Size()
	if b.configBytes > gitSnapshotAuditMaxConfigFile {
		return nil, fmt.Errorf("Git configuration exceeds the size budget")
	}
	return os.ReadFile(path)
}

func (b *gitSnapshotAuditBudget) source() error {
	if err := b.check(); err != nil {
		return err
	}
	b.configSources++
	if b.configSources > gitSnapshotAuditMaxConfigSrc {
		return fmt.Errorf("Git configuration exceeded the source budget")
	}
	return nil
}

func (b *gitSnapshotAuditBudget) alternate() error {
	if err := b.check(); err != nil {
		return err
	}
	b.alternatePaths++
	if b.alternatePaths > gitSnapshotAuditMaxAlternates {
		return fmt.Errorf("Git alternates exceeded the path budget")
	}
	return nil
}

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
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return auditSnapshotGitScopeContext(ctx, plan)
}

func auditSnapshotGitScopeContext(ctx context.Context, plan *snapshotPlan) gitSnapshotAudit {
	if plan == nil {
		return gitSnapshotAudit{Safe: true}
	}
	budget := newGitSnapshotAuditBudget(ctx)
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
		if err := walkGitSnapshotTree(ctx, unit.AbsPath, budget, func(path string, entry fs.DirEntry) error {
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
		if top, ok := gitToplevelContext(ctx, unit.AbsPath); ok {
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
		top, err := gitPathContext(ctx, repo, "--show-toplevel")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git worktree %q: %v", repo, err)}
		}
		gitDir, err := gitPathContext(ctx, repo, "--absolute-git-dir")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git directory for %q: %v", repo, err)}
		}
		commonDir, err := gitPathContext(ctx, repo, "--git-common-dir")
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

		dependencies, err := gitSnapshotDependenciesContext(ctx, budget, repo, gitDir, commonDir)
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
		if err := auditGitMetadataRedirectsContext(ctx, budget, repository.Worktree, repository.GitDir, repository.CommonDir, repository.Dependencies, commonDirs); err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("unsafe Git metadata for %q: %v", repository.Worktree, err)}
		}
	}
	audit.Dependencies = compactContainedPaths(audit.Dependencies)
	audit.Safe = true
	return audit
}

// auditSnapshotGitConfiguration applies only to snapshot delivery. The
// established worktree backend intentionally retains a user's native Git hook
// behavior when the feature gate is off or a snapshot falls back.
func auditSnapshotGitConfiguration(audit gitSnapshotAudit) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return auditSnapshotGitConfigurationContext(ctx, audit)
}

func auditSnapshotGitConfigurationContext(ctx context.Context, audit gitSnapshotAudit) error {
	budget := newGitSnapshotAuditBudget(ctx)
	commonDirs := gitSnapshotCommonDirs(audit.Repositories)
	for _, repository := range audit.Repositories {
		if err := auditGitConfigurationContext(ctx, budget, repository.Worktree, repository.GitDir, repository.CommonDir, repository.Dependencies, commonDirs); err != nil {
			return fmt.Errorf("unsafe Git configuration for %q: %w", repository.Worktree, err)
		}
	}
	return nil
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
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return gitPathContext(ctx, repo, args...)
}

func gitPathContext(ctx context.Context, repo string, args ...string) (string, error) {
	argv := append([]string{"-C", repo, "rev-parse"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
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

type gitConfigEntry struct {
	Origin string
	Key    string
	Value  string
}

// auditGitConfiguration admits only repository/worktree configuration files
// that the snapshot overlays. It recursively follows every include directive,
// including inactive includeIf branches: a branch change inside the snapshot
// must not make a previously unaudited host config effective.
func auditGitConfiguration(worktree, gitDir, commonDir string, dependencies, commonDirs []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return auditGitConfigurationContext(ctx, newGitSnapshotAuditBudget(ctx), worktree, gitDir, commonDir, dependencies, commonDirs)
}

func auditGitConfigurationContext(ctx context.Context, budget *gitSnapshotAuditBudget, worktree, gitDir, commonDir string, dependencies, commonDirs []string) error {
	allowed := compactContainedPaths(append(append([]string{}, dependencies...), worktree))
	forbidden, forbiddenRoots, err := siblingGitWorktreeAdminDirs(commonDirs, gitDir)
	if err != nil {
		return err
	}

	if err := budget.source(); err != nil {
		return err
	}
	entries, err := gitConfigEntriesContext(ctx, worktree, "--includes", "--null", "--show-origin", "--list")
	if err != nil {
		return fmt.Errorf("read effective Git configuration: %w", err)
	}
	type configSource struct {
		path  string
		chain []string
	}
	sources := make([]configSource, 0, len(entries))
	seenSources := make(map[string]bool)
	for _, entry := range entries {
		source, err := auditGitConfigEntrySource(worktree, entry, allowed, forbidden, forbiddenRoots)
		if err != nil {
			return err
		}
		if !seenSources[source] {
			seenSources[source] = true
			sources = append(sources, configSource{path: source, chain: []string{source}})
		}
		if isGitHooksPathConfig(entry.Key) {
			if err := auditGitHooksPath(entry.Value, worktree, allowed, forbidden, forbiddenRoots); err != nil {
				return fmt.Errorf("unsafe core.hooksPath from %q: %w", source, err)
			}
		}
	}

	for len(sources) > 0 {
		source := sources[0]
		sources = sources[1:]
		if err := budget.source(); err != nil {
			return err
		}
		fileEntries, err := gitConfigEntriesContext(ctx, worktree, "--file", source.path, "--includes", "--null", "--show-origin", "--list")
		if err != nil {
			return fmt.Errorf("read Git configuration source %q: %w", source.path, err)
		}
		for _, entry := range fileEntries {
			entrySource, err := auditGitConfigEntrySource(worktree, entry, allowed, forbidden, forbiddenRoots)
			if err != nil {
				return err
			}
			if !seenSources[entrySource] {
				seenSources[entrySource] = true
				sources = append(sources, configSource{path: entrySource, chain: append(append([]string{}, source.chain...), entrySource)})
			}
			if isGitHooksPathConfig(entry.Key) {
				if err := auditGitHooksPath(entry.Value, worktree, allowed, forbidden, forbiddenRoots); err != nil {
					return fmt.Errorf("unsafe core.hooksPath from %q: %w", entrySource, err)
				}
			}
			if !isGitIncludePathConfig(entry.Key) {
				continue
			}
			included, err := resolveGitConfigPath(filepath.Dir(entrySource), entry.Value)
			if err != nil {
				return fmt.Errorf("resolve include from %q: %w", entrySource, err)
			}
			included, err = auditGitConfigFile(included, allowed, forbidden, forbiddenRoots)
			if err != nil {
				return fmt.Errorf("unsafe include from %q: %w", entrySource, err)
			}
			for _, ancestor := range source.chain {
				if included == ancestor {
					return fmt.Errorf("Git configuration include cycle at %q", included)
				}
			}
			if !seenSources[included] {
				seenSources[included] = true
				sources = append(sources, configSource{path: included, chain: append(append([]string{}, source.chain...), included)})
			}
		}
	}
	return nil
}

func gitConfigEntries(repo string, args ...string) ([]gitConfigEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return gitConfigEntriesContext(ctx, repo, args...)
}

func gitConfigEntriesContext(ctx context.Context, repo string, args ...string) ([]gitConfigEntry, error) {
	argv := append([]string{"-C", repo, "config"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = execenv.SnapshotGitEnvironment()
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(raw) > gitSnapshotAuditMaxConfigFile {
		return nil, fmt.Errorf("Git configuration output exceeds the size budget")
	}
	if len(raw) == 0 {
		return nil, nil
	}
	records := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(records)%2 != 0 {
		return nil, fmt.Errorf("malformed Git config output")
	}
	entries := make([]gitConfigEntry, 0, len(records)/2)
	for i := 0; i < len(records); i += 2 {
		origin, record := records[i], records[i+1]
		key, value, ok := strings.Cut(record, "\n")
		if !ok || key == "" || strings.ContainsRune(origin, 0) || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("malformed Git config entry")
		}
		entries = append(entries, gitConfigEntry{Origin: origin, Key: key, Value: value})
	}
	return entries, nil
}

func auditGitConfigEntrySource(repo string, entry gitConfigEntry, allowed, forbidden, forbiddenRoots []string) (string, error) {
	source, ok := strings.CutPrefix(entry.Origin, "file:")
	if !ok || source == "" {
		return "", fmt.Errorf("unsupported Git config origin %q", entry.Origin)
	}
	if !filepath.IsAbs(source) {
		source = filepath.Join(repo, source)
	}
	return auditGitConfigFile(source, allowed, forbidden, forbiddenRoots)
}

func auditGitConfigFile(path string, allowed, forbidden, forbiddenRoots []string) (string, error) {
	resolved, err := auditGitConfigPath(path, allowed, forbidden, forbiddenRoots)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Git config source %q is not a regular file", resolved)
	}
	return resolved, nil
}

func auditGitHooksPath(value, worktree string, allowed, forbidden, forbiddenRoots []string) error {
	path, err := resolveGitConfigPath(worktree, value)
	if err != nil {
		return err
	}
	resolved, err := auditGitConfigPath(path, allowed, forbidden, forbiddenRoots)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("hook path %q is not a directory", resolved)
	}
	return nil
}

func auditGitConfigPath(path string, allowed, forbidden, forbiddenRoots []string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid Git config path %q", path)
	}
	direct := filepath.Clean(path)
	if containsCleanPath(direct, forbiddenRoots) || withinAnyScope(direct, forbidden) || !withinAnyScope(direct, allowed) {
		return "", fmt.Errorf("Git config path %q is outside materialized dependencies", direct)
	}
	resolved, err := filepath.EvalSymlinks(direct)
	if err != nil {
		return "", fmt.Errorf("resolve Git config path %q: %w", direct, err)
	}
	resolved = filepath.Clean(resolved)
	if containsCleanPath(resolved, forbiddenRoots) || withinAnyScope(resolved, forbidden) || !withinAnyScope(resolved, allowed) {
		return "", fmt.Errorf("Git config path %q resolves outside materialized dependencies to %q", direct, resolved)
	}
	return resolved, nil
}

func resolveGitConfigPath(base, value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("invalid Git config path %q", value)
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	} else if strings.HasPrefix(value, "~") {
		return "", fmt.Errorf("unsupported home expansion in %q", value)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value), nil
}

func isGitHooksPathConfig(key string) bool {
	return strings.EqualFold(key, "core.hookspath")
}

func isGitIncludePathConfig(key string) bool {
	key = strings.ToLower(key)
	return key == "include.path" || (strings.HasPrefix(key, "includeif.") && strings.HasSuffix(key, ".path"))
}

func gitSnapshotDependencies(repo, gitDir, commonDir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return gitSnapshotDependenciesContext(ctx, newGitSnapshotAuditBudget(ctx), repo, gitDir, commonDir)
}

func gitSnapshotDependenciesContext(ctx context.Context, budget *gitSnapshotAuditBudget, repo, gitDir, commonDir string) ([]string, error) {
	objectDir, err := gitPathContext(ctx, repo, "--git-path", "objects")
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
		alternates, err := budget.readConfig(filepath.Join(objects, "info", "alternates"))
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
			if err := budget.alternate(); err != nil {
				return nil, err
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
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return auditGitMetadataRedirectsContext(ctx, newGitSnapshotAuditBudget(ctx), worktree, gitDir, commonDir, dependencies, commonDirs)
}

func auditGitMetadataRedirectsContext(ctx context.Context, budget *gitSnapshotAuditBudget, worktree, gitDir, commonDir string, dependencies, commonDirs []string) error {
	writableMetadata := compactContainedPaths([]string{gitDir, commonDir})
	return auditGitMetadataRedirectsWithinContext(ctx, budget, worktree, gitDir, commonDir, dependencies, writableMetadata, dependencies, commonDirs, true)
}

// auditGitMetadataRedirectsWithin audits Git's dependency graph against an
// explicit set of writable metadata paths. alternateAllowed is intentionally
// separate: alternate object databases are read dependencies, but they must
// not authorise refs, indexes, or other metadata redirects to write there.
func auditGitMetadataRedirectsWithin(worktree, gitDir, commonDir string, dependencies, allowed, alternateAllowed, commonDirs []string, allowLinkedWorktreeBacklink bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotAuditTimeout)
	defer cancel()
	return auditGitMetadataRedirectsWithinContext(ctx, newGitSnapshotAuditBudget(ctx), worktree, gitDir, commonDir, dependencies, allowed, alternateAllowed, commonDirs, allowLinkedWorktreeBacklink)
}

func auditGitMetadataRedirectsWithinContext(ctx context.Context, budget *gitSnapshotAuditBudget, worktree, gitDir, commonDir string, dependencies, allowed, alternateAllowed, commonDirs []string, allowLinkedWorktreeBacklink bool) error {
	allowed = compactContainedPaths(allowed)
	alternateAllowed = compactContainedPaths(alternateAllowed)
	commonDirs = uniqueGitPaths(commonDirs)
	forbidden, forbiddenRoots, err := siblingGitWorktreeAdminDirs(commonDirs, gitDir)
	if err != nil {
		return err
	}
	worktreesRoot := filepath.Join(commonDir, "worktrees")
	currentLinkedGitDir := filepath.Dir(gitDir) == worktreesRoot

	if err := budget.check(); err != nil {
		return err
	}
	if err := auditGitMetadataEntry(filepath.Join(worktree, ".git"), worktree, allowed, alternateAllowed, forbidden, forbiddenRoots, true, allowLinkedWorktreeBacklink); err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if err := walkGitSnapshotTree(ctx, dependency, budget, func(path string, entry fs.DirEntry) error {
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

func walkGitSnapshotTree(ctx context.Context, root string, budget *gitSnapshotAuditBudget, visit func(path string, entry fs.DirEntry) error) error {
	rootMountID, mountKnown, err := snapshotPathMountID(root)
	if err != nil {
		return fmt.Errorf("inspect mount boundary for %q: %w", root, err)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := budget.visit(); err != nil {
			return err
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && path != root && mountKnown {
			mountID, _, err := snapshotPathMountID(path)
			if err != nil {
				return fmt.Errorf("inspect mount boundary for %q: %w", path, err)
			}
			if mountID != rootMountID {
				return fmt.Errorf("directory %q crosses a mount boundary", path)
			}
		}
		if err := budget.check(); err != nil {
			return err
		}
		return visit(path, entry)
	})
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
